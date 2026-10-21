package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-deploy-api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func deleteUpstreamObject(w http.ResponseWriter, resource string) {
	kind, version, uid := "Deployment", "apps/v1", "dep-uid"
	if resource == "services" {
		kind, version, uid = "Service", "v1", "svc-uid"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": kind, "apiVersion": version, "metadata": map[string]any{"name": "demo", "namespace": "deploy-api", "uid": uid, "resourceVersion": "7", "labels": map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": "demo"}}})
}

func assertDeleteOptions(t *testing.T, r *http.Request, resource string) metav1.DeleteOptions {
	t.Helper()
	var options metav1.DeleteOptions
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("delete options body: %v", err)
		return options
	}
	if _, _, err := scheme.Codecs.UniversalDeserializer().Decode(body, nil, &options); err != nil {
		t.Errorf("delete options: %v", err)
		return options
	}
	uid := "dep-uid"
	if resource == "services" {
		uid = "svc-uid"
	}
	if options.Preconditions == nil || options.Preconditions.UID == nil || string(*options.Preconditions.UID) != uid || options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != "7" {
		t.Errorf("missing delete identity preconditions: %#v", options)
	}
	if resource == "deployments" && (options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationBackground) {
		t.Errorf("deployment propagation = %#v", options.PropagationPolicy)
	}
	if options.GracePeriodSeconds != nil || options.OrphanDependents != nil || len(options.DryRun) != 0 {
		t.Errorf("unexpected delete options: %#v", options)
	}
	return options
}

func TestDeleteThroughTypedUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, verb, resource, body string
		code                       int
		reason                     metav1.StatusReason
		wantStatus                 int
		want                       kube.ResourceOutcomes
	}{
		{name: "both accepted", wantStatus: 202, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.DeleteRequested}},
		{name: "deployment read denied", verb: "GET", resource: "deployments", code: 403, reason: metav1.StatusReasonForbidden, wantStatus: 503, want: kube.ResourceOutcomes{Deployment: kube.NotAttempted, Service: kube.DeleteRequested}},
		{name: "service read denied", verb: "GET", resource: "services", code: 403, reason: metav1.StatusReasonForbidden, wantStatus: 503, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.NotAttempted}},
		{name: "deployment delete rejected", verb: "DELETE", resource: "deployments", code: 403, reason: metav1.StatusReasonForbidden, wantStatus: 503, want: kube.ResourceOutcomes{Deployment: kube.Failed, Service: kube.DeleteRequested}},
		{name: "service delete rejected", verb: "DELETE", resource: "services", code: 409, reason: metav1.StatusReasonConflict, wantStatus: 409, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.Failed}},
		{name: "deployment disappeared", verb: "DELETE", resource: "deployments", code: 404, reason: metav1.StatusReasonNotFound, wantStatus: 202, want: kube.ResourceOutcomes{Deployment: kube.Absent, Service: kube.DeleteRequested}},
		{name: "service disappeared", verb: "DELETE", resource: "services", code: 404, reason: metav1.StatusReasonNotFound, wantStatus: 202, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.Absent}},
		{name: "deployment delete timeout", verb: "DELETE", resource: "deployments", code: 504, reason: metav1.StatusReasonTimeout, wantStatus: 504, want: kube.ResourceOutcomes{Deployment: kube.Unknown, Service: kube.DeleteRequested}},
		{name: "service server error", verb: "DELETE", resource: "services", code: 500, reason: metav1.StatusReasonInternalError, wantStatus: 500, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.Unknown}},
		{name: "malformed DELETE response", verb: "DELETE", resource: "deployments", body: "malformed content type", wantStatus: 500, want: kube.ResourceOutcomes{Deployment: kube.Unknown, Service: kube.DeleteRequested}},
		{name: "truncated DELETE response", verb: "DELETE", resource: "services", body: "truncated", wantStatus: 503, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.Unknown}},
		{name: "malformed GET response", verb: "GET", resource: "deployments", body: "malformed content type", wantStatus: 500, want: kube.ResourceOutcomes{Deployment: kube.NotAttempted, Service: kube.DeleteRequested}},
		{name: "truncated GET response", verb: "GET", resource: "services", body: "truncated", wantStatus: 503, want: kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.NotAttempted}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			requests := map[string]int{}
			deleted := map[string]bool{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				resource := "deployments"
				path := "/apis/apps/v1/namespaces/deploy-api/deployments/demo"
				if strings.Contains(r.URL.Path, "/services/") {
					resource = "services"
					path = "/api/v1/namespaces/deploy-api/services/demo"
				}
				if r.URL.Path != path || (r.Method != "GET" && r.Method != "DELETE") {
					t.Errorf("unexpected upstream operation: %s %s", r.Method, r.URL.Path)
					upstreamStatus(w, 500, metav1.StatusReasonInternalError, "unexpected operation")
					return
				}
				requests[r.Method+" "+resource]++
				if r.Method == "DELETE" {
					assertDeleteOptions(t, r, resource)
				}
				if r.Method == tc.verb && resource == tc.resource {
					if tc.body != "" {
						if r.Method == "DELETE" {
							deleted[resource] = true
						}
						w.Header().Set("Content-Type", "application/json")
						if tc.body == "truncated" {
							w.Header().Set("Content-Length", "1000")
						} else {
							w.Header().Set("Content-Type", "application/json; invalid")
						}
						w.WriteHeader(200)
						_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Success"}`)
						return
					}
					upstreamStatus(w, tc.code, tc.reason, "controlled Kubernetes response")
					return
				}
				if r.Method == "GET" {
					deleteUpstreamObject(w, resource)
					return
				}
				deleted[resource] = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Success"}`)
			}))
			defer upstream.Close()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/demo", nil))
			if w.Code != tc.wantStatus {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
			var outcomes kube.ResourceOutcomes
			if w.Code == 202 {
				var result kube.DeleteResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				outcomes = result.Resources
				if w.Header().Get("Location") != "/apps/demo" {
					t.Fatalf("Location = %q", w.Header().Get("Location"))
				}
			} else {
				var envelope errorEnvelope
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				outcomes = *envelope.Error.Details.Resources
				if w.Header().Get("Location") != "" || envelope.Error.Details.Failures == nil {
					t.Fatalf("error = %#v", envelope.Error)
				}
				failure := envelope.Error.Details.Failures.Deployment
				if tc.resource == "services" {
					failure = envelope.Error.Details.Failures.Service
				}
				if failure == nil {
					t.Fatalf("missing resource error: %s", w.Body)
				}
				if tc.reason != "" && (failure.Reason != string(tc.reason) || failure.Message != "controlled Kubernetes response") {
					t.Fatalf("resource reason lost: %#v", failure)
				}
				if strings.Contains(w.Body.String(), upstream.URL) {
					t.Fatalf("upstream URL leaked: %s", w.Body)
				}
			}
			if outcomes != tc.want {
				t.Fatalf("outcomes = %#v, want %#v", outcomes, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, resource := range []string{"deployments", "services"} {
				if requests["GET "+resource] != 1 {
					t.Fatalf("resource was not read once: %v", requests)
				}
				wantDeletes := 1
				if tc.verb == "GET" && tc.resource == resource {
					wantDeletes = 0
				}
				if requests["DELETE "+resource] != wantDeletes {
					t.Fatalf("delete attempts = %v", requests)
				}
				if tc.body != "" && tc.verb == "DELETE" && resource == tc.resource && !deleted[resource] {
					t.Fatal("uncertain response did not follow an applied deletion")
				}
			}
		})
	}
}

func TestDeleteRacePreconditionsPreserveChangedResource(t *testing.T) {
	for _, resource := range []string{"deployments", "services"} {
		for _, race := range []string{"UID replacement", "ownership changed at new resourceVersion"} {
			t.Run(resource+" "+race, func(t *testing.T) {
				var mu sync.Mutex
				retained, companionDeleted := false, false
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					current := "deployments"
					if strings.Contains(r.URL.Path, "/services/") {
						current = "services"
					}
					if r.Method == "GET" {
						deleteUpstreamObject(w, current)
						return
					}
					if r.Method != "DELETE" {
						t.Errorf("unexpected mutation: %s", r.Method)
						return
					}
					options := assertDeleteOptions(t, r, current)
					if current == resource {
						currentUID, currentVersion := "dep-uid", "7"
						if current == "services" {
							currentUID = "svc-uid"
						}
						if race == "UID replacement" {
							currentUID = "replacement-uid"
						} else {
							currentVersion = "8"
						}
						if options.Preconditions != nil && options.Preconditions.UID != nil && options.Preconditions.ResourceVersion != nil && (string(*options.Preconditions.UID) != currentUID || *options.Preconditions.ResourceVersion != currentVersion) {
							retained = true
							upstreamStatus(w, 409, metav1.StatusReasonConflict, "delete precondition did not match current resource")
							return
						}
						t.Error("changed resource would have been deleted")
					} else {
						companionDeleted = true
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Success"}`)
				}))
				defer upstream.Close()
				client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/demo", nil))
				var envelope errorEnvelope
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				want := kube.ResourceOutcomes{Deployment: kube.Failed, Service: kube.DeleteRequested}
				if resource == "services" {
					want = kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.Failed}
				}
				if w.Code != 409 || *envelope.Error.Details.Resources != want {
					t.Fatalf("response = %d %s", w.Code, w.Body)
				}
				mu.Lock()
				defer mu.Unlock()
				if !retained || !companionDeleted {
					t.Fatalf("changed resource retained = %v, companion deleted = %v", retained, companionDeleted)
				}
			})
		}
	}
}

func TestDeleteSharedDeadlineCancelsUpstream(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource := "deployments"
		if strings.Contains(r.URL.Path, "/services/") {
			resource = "services"
		}
		if r.Method == "GET" {
			deleteUpstreamObject(w, resource)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if resource == "deployments" {
			select {
			case <-time.After(40 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Success"}`)
			return
		}
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	var mu sync.Mutex
	var deadlines []time.Time
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
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/demo", nil).WithContext(ctx))
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if w.Code != 504 || *envelope.Error.Details.Resources != (kube.ResourceOutcomes{Deployment: kube.DeleteRequested, Service: kube.Unknown}) {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("timed out upstream request was not canceled")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 4 {
		t.Fatalf("upstream calls = %d", len(deadlines))
	}
	for _, deadline := range deadlines {
		if !deadline.Equal(deadlines[0]) {
			t.Fatalf("operation deadline changed: %v", deadlines)
		}
	}
}

func TestDeleteDefaultDeadlineAndTransportSanitization(t *testing.T) {
	var deadlines []time.Time
	client, err := kubernetes.NewForConfig(&rest.Config{Host: "http://controlled.invalid", WrapTransport: func(http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Error("upstream request has no deadline")
			}
			deadlines = append(deadlines, deadline)
			return nil, &url.Error{Op: r.Method, URL: "https://user:secret@private.invalid", Err: errors.New("private transport details")}
		})
	}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/demo", nil))
	var envelope errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || *envelope.Error.Details.Resources != (kube.ResourceOutcomes{Deployment: kube.NotAttempted, Service: kube.NotAttempted}) || envelope.Error.Details.Failures.Deployment == nil || envelope.Error.Details.Failures.Service == nil {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{"secret", "private.invalid", "private transport", "controlled.invalid"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("transport details leaked: %s", w.Body)
		}
	}
	if len(deadlines) != 2 {
		t.Fatalf("upstream calls = %d", len(deadlines))
	}
	for _, deadline := range deadlines {
		if !deadline.Equal(deadlines[0]) || deadline.Before(start.Add(10*time.Second)) || deadline.After(time.Now().Add(10*time.Second)) {
			t.Fatalf("incorrect deadline = %v", deadline)
		}
	}
}
