package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s-deploy-api/internal/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func (f creatorFunc) Logs(context.Context, string, string, int64) (io.ReadCloser, error) {
	return nil, errors.New("unexpected logs")
}
func (f deleterFunc) Logs(context.Context, string, string, int64) (io.ReadCloser, error) {
	return nil, errors.New("unexpected logs")
}
func (f inspectorFunc) Logs(context.Context, string, string, int64) (io.ReadCloser, error) {
	return nil, errors.New("unexpected logs")
}

type loggerFunc func(context.Context, string, string, int64) (io.ReadCloser, error)

func (f loggerFunc) Logs(ctx context.Context, name, pod string, tail int64) (io.ReadCloser, error) {
	return f(ctx, name, pod, tail)
}

func TestLogsHTTPValidation(t *testing.T) {
	for _, query := range []string{"", "pod=", "pod=bad/name", "pod=demo&pod=other", "pod=demo&tailLines=", "pod=demo&tailLines=-1", "pod=demo&tailLines=1001", "pod=demo&tailLines=1.5", "pod=demo&tailLines=1;bad", "pod=demo&tailLines=100&tailLines=0"} {
		t.Run(query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/apps/demo/logs?"+query, nil)
			req.SetPathValue("name", "demo")
			w := httptest.NewRecorder()
			appLogs(loggerFunc(func(context.Context, string, string, int64) (io.ReadCloser, error) {
				t.Fatal("invalid query reached upstream")
				return nil, nil
			}))(w, req)
			if w.Code != 400 || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	for _, tc := range []struct {
		query string
		tail  int64
	}{{"pod=demo", 100}, {"pod=demo&tailLines=0", 0}, {"pod=demo&tailLines=100", 100}, {"pod=demo&tailLines=1000", 1000}} {
		req := httptest.NewRequest("GET", "/apps/demo/logs?"+tc.query, nil)
		req.SetPathValue("name", "demo")
		w := httptest.NewRecorder()
		appLogs(loggerFunc(func(_ context.Context, name, pod string, tail int64) (io.ReadCloser, error) {
			if name != "demo" || pod != "demo" || tail != tc.tail {
				t.Fatalf("args=%s %s %d", name, pod, tail)
			}
			return io.NopCloser(strings.NewReader("line\n")), nil
		}))(w, req)
		if w.Code != 200 || !w.Flushed || w.Body.String() != "line\n" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}

func logHTTPUpstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/log") {
			handler(w, r)
			return
		}
		meta := metav1.ObjectMeta{Name: "demo", Namespace: kube.Namespace, UID: "deployment-uid", Labels: map[string]string{"app.kubernetes.io/name": "demo", "app.kubernetes.io/managed-by": "deploy-api"}}
		control := true
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments/demo"):
			_ = json.NewEncoder(w).Encode(appsv1.Deployment{ObjectMeta: meta})
		case strings.HasSuffix(r.URL.Path, "/replicasets"):
			meta.Name = "demo-rs"
			meta.UID = "rs-uid"
			meta.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "demo", UID: "deployment-uid", Controller: &control}}
			_ = json.NewEncoder(w).Encode(appsv1.ReplicaSetList{Items: []appsv1.ReplicaSet{{ObjectMeta: meta}}})
		case strings.HasSuffix(r.URL.Path, "/pods/demo-pod"):
			meta.Name = "demo-pod"
			meta.UID = "pod-uid"
			meta.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "demo-rs", UID: "rs-uid", Controller: &control}}
			_ = json.NewEncoder(w).Encode(corev1.Pod{ObjectMeta: meta})
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
	api := httptest.NewServer(NewHandler(kube.Apps{Client: client}))
	t.Cleanup(api.Close)
	return api
}

func TestLogsHTTPPreHeaderErrors(t *testing.T) {
	for _, tc := range []struct {
		status, want int
		reason       metav1.StatusReason
	}{{400, 409, metav1.StatusReasonBadRequest}, {403, 503, metav1.StatusReasonForbidden}, {404, 404, metav1.StatusReasonNotFound}, {504, 504, metav1.StatusReasonTimeout}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			api := logHTTPUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				upstreamStatus(w, tc.status, tc.reason, "container logs unavailable")
			})
			response, err := http.Get(api.URL + "/apps/demo/logs?pod=demo-pod")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var envelope errorEnvelope
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want || response.Header.Get("Content-Type") != "application/json" || envelope.Error.Message != "container logs unavailable" {
				t.Fatalf("%d %#v", response.StatusCode, envelope)
			}
		})
	}
}

func TestLogsHTTPFlushAndDisconnect(t *testing.T) {
	canceled := make(chan struct{})
	release := make(chan struct{})
	api := logHTTPUpstream(t, func(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", api.URL+"/apps/demo/logs?pod=demo-pod", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("response=%v", response)
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); line != "first\n" || err != nil {
		t.Fatalf("first=%q %v", line, err)
	}
	close(release)
	if line, err := reader.ReadString('\n'); line != "second\n" || err != nil {
		t.Fatalf("second=%q %v", line, err)
	}
	_ = response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP disconnect did not cancel upstream")
	}
}

func TestLogsHTTPEOFAndPostHeaderFailure(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "EOF", true: "truncated body"}[broken], func(t *testing.T) {
			var messages strings.Builder
			old := log.Writer()
			log.SetOutput(&messages)
			defer log.SetOutput(old)
			api := logHTTPUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if broken {
					w.Header().Set("Content-Length", "100")
				}
				_, _ = io.WriteString(w, "one\n")
			})
			response, err := http.Get(api.URL + "/apps/demo/logs?pod=demo-pod")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != 200 || string(body) != "one\n" {
				t.Fatalf("body=%q status=%d error=%v", body, response.StatusCode, err)
			}
			if broken && !strings.Contains(messages.String(), "log stream upstream read failed") {
				t.Fatalf("missing process log %q", messages.String())
			}
			if !broken && messages.Len() != 0 {
				t.Fatalf("EOF logged as failure %q", messages.String())
			}
		})
	}
}

func TestLogsHTTPDeliveryAndCancellationBudget(t *testing.T) {
	sent := make(chan time.Time)
	canceled := make(chan time.Time, 1)
	api := logHTTPUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for stamp := range sent {
			_, _ = io.WriteString(w, stamp.Format(time.RFC3339Nano)+"\n")
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
		canceled <- time.Now()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", api.URL+"/apps/demo/logs?pod=demo-pod", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var maximum time.Duration
	for i := 0; i < 20; i++ {
		stamp := time.Now()
		sent <- stamp
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(line))
		if err != nil || !parsed.Equal(stamp) {
			t.Fatalf("timestamp=%q error=%v", line, err)
		}
		elapsed := time.Since(stamp)
		if elapsed > maximum {
			maximum = elapsed
		}
		if elapsed > time.Second {
			t.Fatalf("delivery=%v", elapsed)
		}
		t.Logf("sample=%d delivery=%v", i, elapsed)
	}
	close(sent)
	start := time.Now()
	_ = response.Body.Close()
	select {
	case stamp := <-canceled:
		elapsed := stamp.Sub(start)
		if elapsed > time.Second {
			t.Fatalf("cancellation=%v", elapsed)
		}
		t.Logf("20 lines delivered max=%v cancellation=%v", maximum, elapsed)
	case <-time.After(time.Second):
		t.Fatal("cancellation exceeded budget")
	}
}
