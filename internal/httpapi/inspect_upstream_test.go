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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var inspectionPaths = []string{"/apis/apps/v1/namespaces/deploy-api/deployments/demo", "/api/v1/namespaces/deploy-api/services/demo", "/apis/apps/v1/namespaces/deploy-api/replicasets", "/api/v1/namespaces/deploy-api/pods"}

func inspectionUpstreamObject(path string) any {
	meta := metav1.ObjectMeta{Name: "demo", Namespace: kube.Namespace, UID: "deployment-uid", Labels: map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": "demo"}}
	switch path {
	case inspectionPaths[0]:
		return &appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: meta}
	case inspectionPaths[1]:
		return &corev1.Service{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"}, ObjectMeta: meta}
	case inspectionPaths[2]:
		return &appsv1.ReplicaSetList{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "ReplicaSetList"}, Items: []appsv1.ReplicaSet{}}
	default:
		return &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: []corev1.Pod{}}
	}
}

func TestInspectTypedUpstreamReadsAndErrors(t *testing.T) {
	for _, stop := range append([]string{""}, inspectionPaths...) {
		for _, status := range []int{403, 404, 500, 503, 504} {
			if stop == "" && status != 403 {
				continue
			}
			t.Run(stop+http.StatusText(status), func(t *testing.T) {
				var calls []string
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, r.URL.Path)
					if r.Method != http.MethodGet {
						t.Errorf("unexpected write %s", r.Method)
					}
					if strings.HasSuffix(r.URL.Path, "/replicasets") || strings.HasSuffix(r.URL.Path, "/pods") {
						if r.URL.Query().Get("labelSelector") != "app.kubernetes.io/managed-by=deploy-api,app.kubernetes.io/name=demo" {
							t.Errorf("selector %s", r.URL.RawQuery)
						}
					}
					if r.URL.Path == stop {
						reason := metav1.StatusReasonInternalError
						switch status {
						case 403:
							reason = metav1.StatusReasonForbidden
						case 404:
							reason = metav1.StatusReasonNotFound
						case 503:
							reason = metav1.StatusReasonServiceUnavailable
						case 504:
							reason = metav1.StatusReasonTimeout
						}
						upstreamStatus(w, status, reason, "controlled read failure")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(inspectionUpstreamObject(r.URL.Path))
				}))
				defer upstream.Close()
				client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/demo", nil))
				want := 200
				if stop != "" {
					switch status {
					case 403, 503:
						want = 503
					case 500:
						want = 500
					case 504:
						want = 504
					case 404:
						if stop == inspectionPaths[2] || stop == inspectionPaths[3] {
							want = 503
						}
					}
				}
				if w.Code != want {
					t.Fatalf("response %d want %d %s", w.Code, want, w.Body)
				}
				if want != 200 && (!strings.Contains(w.Body.String(), "controlled read failure") || strings.Contains(w.Body.String(), `"pods"`)) {
					t.Fatalf("misleading response %s", w.Body)
				}
				if stop == "" && len(calls) != 4 {
					t.Fatalf("calls %v", calls)
				}
			})
		}
	}
}

func TestInspectSharesDeadlineAndCancelsUpstream(t *testing.T) {
	for _, stop := range inspectionPaths {
		t.Run(stop, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			canceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == stop {
					<-r.Context().Done()
					close(canceled)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(inspectionUpstreamObject(r.URL.Path))
			}))
			defer upstream.Close()
			var mu sync.Mutex
			var deadlines []time.Time
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
				return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("missing operation deadline")
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
			NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/demo", nil).WithContext(ctx))
			if w.Code != 504 {
				t.Fatalf("response %d %s", w.Code, w.Body)
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream not canceled")
			}
			want, _ := ctx.Deadline()
			mu.Lock()
			defer mu.Unlock()
			for _, got := range deadlines {
				if !got.Equal(want) {
					t.Fatalf("deadline %s want %s", got, want)
				}
			}
		})
	}
}

func TestInspectDefaultDeadlineAndTransportSanitization(t *testing.T) {
	start := time.Now()
	var deadline time.Time
	calls := 0
	client, err := kubernetes.NewForConfig(&rest.Config{Host: "https://user:secret@controlled.invalid", WrapTransport: func(http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			got, ok := r.Context().Deadline()
			if !ok {
				t.Error("missing deadline")
			}
			if calls == 0 {
				deadline = got
			} else if !got.Equal(deadline) {
				t.Error("deadline reset")
			}
			calls++
			if r.URL.Path == inspectionPaths[3] {
				return nil, errors.New("secret credentials")
			}
			body, err := json.Marshal(inspectionUpstreamObject(r.URL.Path))
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})
	}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/demo", nil))
	if calls != 4 || deadline.Before(start.Add(9*time.Second)) || deadline.After(start.Add(11*time.Second)) {
		t.Fatalf("calls %d deadline %s", calls, deadline)
	}
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "controlled.invalid") {
		t.Fatalf("response %d %s", w.Code, w.Body)
	}
}
