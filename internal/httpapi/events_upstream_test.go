package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-deploy-api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const eventsPath = "/api/v1/namespaces/deploy-api/events"

func TestInspectEventsHTTP(t *testing.T) {
	for _, scenario := range []string{"success", "forbidden", "timeout", "transport"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			var mu sync.Mutex
			var deadlines []time.Time
			canceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == eventsPath {
					if r.URL.Query().Get("fieldSelector") != "type=Warning" {
						t.Errorf("selector %s", r.URL.RawQuery)
					}
					switch scenario {
					case "forbidden":
						upstreamStatus(w, 403, metav1.StatusReasonForbidden, "warning events access denied")
						return
					case "timeout":
						<-r.Context().Done()
						close(canceled)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&corev1.EventList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "EventList"}, Items: []corev1.Event{
						{ObjectMeta: metav1.ObjectMeta{Name: "owned", Namespace: kube.Namespace}, Type: corev1.EventTypeWarning, InvolvedObject: corev1.ObjectReference{UID: "deployment-uid"}, Reason: "FailedCreate", Message: "replica creation failed"},
						{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: kube.Namespace}, Type: corev1.EventTypeWarning, InvolvedObject: corev1.ObjectReference{UID: "old-uid"}, Message: "unrelated"},
					}})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(inspectionUpstreamObject(r.URL.Path))
			}))
			defer upstream.Close()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
				return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("missing shared deadline")
					}
					mu.Lock()
					deadlines = append(deadlines, deadline)
					mu.Unlock()
					if scenario == "transport" && r.URL.Path == eventsPath {
						return nil, errors.New("https://user:secret@upstream.invalid private credentials")
					}
					return next.RoundTrip(r)
				})
			}})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			start := time.Now()
			NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/demo", nil).WithContext(ctx))
			if w.Code != 200 {
				t.Fatalf("core lost %d %s", w.Code, w.Body)
			}
			var got kube.InspectResult
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Deployment == nil || !got.Resources.Deployment || !got.Resources.Service || got.Pods == nil || got.WarningEvents == nil {
				t.Fatalf("core response %#v", got)
			}
			if scenario == "success" {
				if got.DiagnosticsIncomplete || got.DiagnosticError != nil || len(got.WarningEvents) != 1 || got.WarningEvents[0].Reason != "FailedCreate" {
					t.Fatalf("warnings %#v", got)
				}
			} else {
				if !got.DiagnosticsIncomplete || got.DiagnosticError == nil || len(got.WarningEvents) != 0 || got.DiagnosticError.Message == "" {
					t.Fatalf("degradation %#v", got)
				}
				if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "upstream.invalid") {
					t.Fatalf("credential leak %s", w.Body)
				}
				if scenario == "forbidden" && !strings.Contains(got.DiagnosticError.Message, "access denied") {
					t.Fatalf("diagnostic %#v", got.DiagnosticError)
				}
				if scenario == "timeout" {
					if got.DiagnosticError.Code != "upstream_timeout" || time.Since(start) > time.Second {
						t.Fatalf("timeout %#v", got)
					}
					select {
					case <-canceled:
					case <-time.After(time.Second):
						t.Fatal("upstream not canceled")
					}
				}
			}
			want, _ := ctx.Deadline()
			mu.Lock()
			defer mu.Unlock()
			if len(deadlines) != 5 {
				t.Fatalf("reads %d want 5", len(deadlines))
			}
			for _, deadline := range deadlines {
				if !deadline.Equal(want) {
					t.Fatalf("deadline reset %s != %s", deadline, want)
				}
			}
		})
	}
}
