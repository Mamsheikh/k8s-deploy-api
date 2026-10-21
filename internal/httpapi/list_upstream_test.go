package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-deploy-api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestListRequiredReadsThroughTypedUpstream(t *testing.T) {
	for _, kind := range []string{"deployments", "services"} {
		for _, tc := range []struct {
			name      string
			code      int
			reason    metav1.StatusReason
			want      int
			errorCode string
		}{
			{"not found", 404, metav1.StatusReasonNotFound, 503, "upstream_unavailable"},
			{"forbidden", 403, metav1.StatusReasonForbidden, 503, "upstream_unavailable"},
			{"unavailable", 503, metav1.StatusReasonServiceUnavailable, 503, "upstream_unavailable"},
			{"timeout", 504, metav1.StatusReasonTimeout, 504, "upstream_timeout"},
			{"internal", 500, metav1.StatusReasonInternalError, 500, "internal_error"},
		} {
			t.Run(kind+" "+tc.name, func(t *testing.T) {
				calls := []string{}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, r.URL.Path)
					if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/namespaces/deploy-api/") {
						t.Errorf("unexpected upstream request = %s %s", r.Method, r.URL.Path)
					}
					if r.URL.Query().Get("labelSelector") != "app.kubernetes.io/managed-by=deploy-api,app.kubernetes.io/name" {
						t.Errorf("selector = %q", r.URL.Query().Get("labelSelector"))
					}
					if strings.HasSuffix(r.URL.Path, "/"+kind) {
						upstreamStatus(w, tc.code, tc.reason, "controlled read rejection")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","items":[{"metadata":{"name":"partial","namespace":"deploy-api","labels":{"app.kubernetes.io/managed-by":"deploy-api","app.kubernetes.io/name":"partial"}}}]}`)
				}))
				defer upstream.Close()
				client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps", nil))
				var envelope errorEnvelope
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.want || envelope.Error.Code != tc.errorCode || envelope.Error.Message != "controlled read rejection" || envelope.Error.Details.Reason != string(tc.reason) || envelope.Error.Details.Resources != nil || strings.Contains(w.Body.String(), `"apps"`) || strings.Contains(w.Body.String(), `"resources"`) {
					t.Fatalf("response = %d %s", w.Code, w.Body)
				}
				wantCalls := 1
				if kind == "services" {
					wantCalls = 2
				}
				if len(calls) != wantCalls {
					t.Fatalf("calls = %v", calls)
				}
			})
		}
	}
}

func TestListTransportErrorIsSanitized(t *testing.T) {
	client, err := kubernetes.NewForConfig(&rest.Config{Host: "https://user:secret@controlled.invalid", WrapTransport: func(http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("credential=secret") })
	}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "controlled.invalid") || strings.Contains(w.Body.String(), "inspect before retrying") || strings.Contains(w.Body.String(), `"resources"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
}

func TestListSharesDeadlineAndCancelsTypedUpstream(t *testing.T) {
	for _, stop := range []string{"deadline on deployment", "deadline on service", "cancel on service"} {
		t.Run(stop, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			canceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/deployments") && stop != "deadline on deployment" {
					select {
					case <-time.After(30 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","items":[]}`)
					return
				}
				if stop == "cancel on service" {
					cancel()
				}
				<-r.Context().Done()
				close(canceled)
			}))
			defer upstream.Close()
			var mu sync.Mutex
			deadlines := []time.Time{}
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
				return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("upstream request has no deadline")
					}
					mu.Lock()
					deadlines = append(deadlines, deadline)
					mu.Unlock()
					return next.RoundTrip(r)
				})
			}})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps", nil).WithContext(ctx))
			wantStatus := 504
			if stop == "cancel on service" {
				wantStatus = 503
			}
			if w.Code != wantStatus || strings.Contains(w.Body.String(), `"apps"`) || strings.Contains(w.Body.String(), `"resources"`) || strings.Contains(w.Body.String(), "inspect before retrying") {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request was not canceled")
			}
			mu.Lock()
			defer mu.Unlock()
			wantCalls := 2
			if stop == "deadline on deployment" {
				wantCalls = 1
			}
			if len(deadlines) != wantCalls {
				t.Fatalf("upstream calls = %d, want %d", len(deadlines), wantCalls)
			}
			incomingDeadline, _ := ctx.Deadline()
			for _, deadline := range deadlines {
				if !deadline.Equal(incomingDeadline) {
					t.Fatalf("upstream deadline = %v, want incoming %v", deadline, incomingDeadline)
				}
			}
		})
	}
}

func TestListDefaultDeadlineIsTenSeconds(t *testing.T) {
	deadlines := []time.Time{}
	client, err := kubernetes.NewForConfig(&rest.Config{Host: "http://controlled.invalid", WrapTransport: func(http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Error("upstream request has no deadline")
			}
			deadlines = append(deadlines, deadline)
			kind, version := "DeploymentList", "apps/v1"
			if strings.HasSuffix(r.URL.Path, "/services") {
				kind, version = "ServiceList", "v1"
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"apiVersion":"` + version + `","kind":"` + kind + `","items":[]}`))}, nil
		})
	}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps", nil))
	if w.Code != 200 || len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("response = %d %s, deadlines = %v", w.Code, w.Body, deadlines)
	}
	if budget := deadlines[0].Sub(start); budget < 9*time.Second || budget > kube.OperationTimeout+time.Second {
		t.Fatalf("operation budget = %v", budget)
	}
}
