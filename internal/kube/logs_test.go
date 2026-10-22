package kube

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func logTestClient(t *testing.T, scenario string, logHandler http.HandlerFunc) Apps {
	t.Helper()
	d, rs, p := inspectionFixture()
	if scenario == "unmanaged" {
		delete(d.Labels, "app.kubernetes.io/managed-by")
	}
	if scenario == "unrelated" {
		p.OwnerReferences[0].UID = "other-rs"
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			logHandler(w, r)
		case strings.HasSuffix(r.URL.Path, "/deployments/demo"):
			if scenario == "slow lookup" {
				time.Sleep(60 * time.Millisecond)
			}
			if scenario == "absent app" {
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: 404, Reason: metav1.StatusReasonNotFound})
				return
			}
			_ = json.NewEncoder(w).Encode(d)
		case strings.HasSuffix(r.URL.Path, "/replicasets"):
			_ = json.NewEncoder(w).Encode(appsv1.ReplicaSetList{Items: []appsv1.ReplicaSet{*rs}})
		case strings.HasSuffix(r.URL.Path, "/pods/demo-pod"):
			if scenario == "absent pod" {
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: 404, Reason: metav1.StatusReasonNotFound})
				return
			}
			_ = json.NewEncoder(w).Encode(p)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(upstream.Close)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, QPS: 100, Burst: 100})
	if err != nil {
		t.Fatal(err)
	}
	return Apps{Client: client}
}

func TestLogsOwnershipBeforeUpstream(t *testing.T) {
	for _, tc := range []struct{ scenario, code string }{{"unmanaged", "conflict"}, {"unrelated", "conflict"}, {"absent app", "not_found"}, {"absent pod", "not_found"}} {
		t.Run(tc.scenario, func(t *testing.T) {
			var calls atomic.Int32
			apps := logTestClient(t, tc.scenario, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			body, err := apps.Logs(context.Background(), "demo", "demo-pod", 100)
			failure, ok := err.(*AppError)
			if body != nil || !ok || failure.Code != tc.code || calls.Load() != 0 {
				t.Fatalf("body=%v error=%v calls=%d", body, err, calls.Load())
			}
		})
	}
}

func TestLogsSetupErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		reason metav1.StatusReason
		code   string
	}{{400, metav1.StatusReasonBadRequest, "conflict"}, {403, metav1.StatusReasonForbidden, "upstream_unavailable"}, {404, metav1.StatusReasonNotFound, "not_found"}, {504, metav1.StatusReasonTimeout, "upstream_timeout"}} {
		t.Run(tc.code+string(tc.reason), func(t *testing.T) {
			apps := logTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: int32(tc.status), Reason: tc.reason, Message: "controlled log rejection"})
			})
			body, err := apps.Logs(context.Background(), "demo", "demo-pod", 100)
			failure, ok := err.(*AppError)
			if body != nil || !ok || failure.Code != tc.code || failure.Message != "controlled log rejection" {
				t.Fatalf("body=%v error=%#v", body, err)
			}
		})
	}
}

func TestLogsEstablishmentTimeout(t *testing.T) {
	canceled := make(chan struct{})
	apps := logTestClient(t, "slow lookup", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); close(canceled) })
	start := time.Now()
	body, err := apps.logs(context.Background(), "demo", "demo-pod", 100, 100*time.Millisecond)
	failure, ok := err.(*AppError)
	if body != nil || !ok || failure.Code != "upstream_timeout" {
		t.Fatalf("body=%v error=%#v", body, err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("setup timeout did not cancel upstream")
	}
	if time.Since(start) > time.Second {
		t.Fatal("setup timeout exceeded tolerance")
	}
}

func TestLogsSurviveSetupBudgetAndCancel(t *testing.T) {
	for _, closeBody := range []bool{false, true} {
		t.Run(map[bool]string{false: "request cancellation", true: "body close"}[closeBody], func(t *testing.T) {
			release := make(chan struct{})
			canceled := make(chan struct{})
			apps := logTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("container") != "app" || r.URL.Query().Get("follow") != "true" || r.URL.Query().Get("tailLines") != "0" {
					t.Errorf("query=%s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, "first\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					close(canceled)
					return
				}
				_, _ = io.WriteString(w, "second\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(canceled)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body, err := apps.logs(ctx, "demo", "demo-pod", 0, 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			reader := bufio.NewReader(body)
			if line, err := reader.ReadString('\n'); line != "first\n" || err != nil {
				t.Fatalf("first=%q %v", line, err)
			}
			time.Sleep(150 * time.Millisecond)
			close(release)
			if line, err := reader.ReadString('\n'); line != "second\n" || err != nil {
				t.Fatalf("second=%q %v", line, err)
			}
			if closeBody {
				_ = body.Close()
			} else {
				cancel()
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream was not canceled")
			}
		})
	}
}

func TestLogsStalledErrorBodyIsBounded(t *testing.T) {
	canceled := make(chan struct{})
	apps := logTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	})
	body, err := apps.logs(context.Background(), "demo", "demo-pod", 100, 100*time.Millisecond)
	failure, ok := err.(*AppError)
	if body != nil || !ok || failure.Code != "upstream_timeout" {
		t.Fatalf("body=%v error=%#v", body, err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("stalled error body was not canceled")
	}
}
