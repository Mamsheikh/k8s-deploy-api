package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-deploy-api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func upstreamStatus(w http.ResponseWriter, code int, reason metav1.StatusReason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Code: int32(code), Reason: reason, Message: message})
}

func TestCreateThroughTypedUpstream(t *testing.T) {
	for _, tc := range []struct {
		name             string
		code             int
		reason           metav1.StatusReason
		wantStatus       int
		wantOutcome      kube.CreateOutcome
		acceptedResponse string
	}{
		{"service forbidden", 403, metav1.StatusReasonForbidden, 503, kube.Failed, ""},
		{"namespace disappeared", 404, metav1.StatusReasonNotFound, 404, kube.Failed, ""},
		{"concurrent service collision", 409, metav1.StatusReasonAlreadyExists, 409, kube.Failed, ""},
		{"service unavailable", 503, metav1.StatusReasonServiceUnavailable, 503, kube.Failed, ""},
		{"service server timeout", 504, metav1.StatusReasonTimeout, 504, kube.Unknown, ""},
		// Typed clients use InternalError for both server failures and local decode failures after an accepted write.
		{"service internal failure", 500, metav1.StatusReasonInternalError, 500, kube.Unknown, ""},
		{"service connection lost", 0, "", 503, kube.Unknown, ""},
		{"service truncated accepted response", 201, "", 503, kube.Unknown, "truncated"},
		{"service malformed accepted content type", 201, "", 500, kube.Unknown, "malformed content type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			retained := false
			serviceApplied := false
			calls := []string{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Method == http.MethodGet {
					upstreamStatus(w, 404, metav1.StatusReasonNotFound, "resource absent")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/deployments") {
					retained = true
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(201)
					_, _ = io.WriteString(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"demo","namespace":"deploy-api"}}`)
					return
				}
				if tc.acceptedResponse != "" {
					serviceApplied = true
					w.Header().Set("Content-Type", "application/json")
					if tc.acceptedResponse == "truncated" {
						w.Header().Set("Content-Length", "1000")
					} else {
						w.Header().Set("Content-Type", "application/json; invalid")
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"kind":"Service","apiVersion":"v1","metadata":{"name":"demo"}}`)
					return
				}
				if tc.code == 0 {
					serviceApplied = true
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				upstreamStatus(w, tc.code, tc.reason, "controlled Kubernetes rejection")
			}))
			defer upstream.Close()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
			if err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(NewHandler(kube.Apps{Client: client}))
			defer api.Close()
			response, err := http.Post(api.URL+"/apps", "application/json", strings.NewReader(`{"name":"demo","image":"nginx","containerPort":80}`))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var envelope errorEnvelope
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.wantStatus || response.Header.Get("Location") != "" || *envelope.Error.Details.Resources != (kube.CreateOutcomes{Deployment: kube.Created, Service: tc.wantOutcome}) {
				t.Fatalf("response = %d %#v", response.StatusCode, envelope)
			}
			if tc.code >= 400 && (envelope.Error.Details.Reason != string(tc.reason) || envelope.Error.Message != "controlled Kubernetes rejection") {
				t.Fatalf("missing upstream details: %#v", envelope.Error)
			}
			if strings.Contains(envelope.Error.Message, upstream.URL) {
				t.Fatalf("transport URL leaked: %v", envelope.Error)
			}
			mu.Lock()
			defer mu.Unlock()
			if !retained {
				t.Fatal("deployment was not retained")
			}
			if (tc.code == 0 || tc.acceptedResponse != "") && !serviceApplied {
				t.Fatal("scenario did not simulate an accepted write")
			}
			want := []string{"GET /apis/apps/v1/namespaces/deploy-api/deployments/demo", "GET /api/v1/namespaces/deploy-api/services/demo", "POST /apis/apps/v1/namespaces/deploy-api/deployments", "POST /api/v1/namespaces/deploy-api/services"}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreateSharesDeadlineAndCancelsUpstream(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodGet {
			upstreamStatus(w, 404, metav1.StatusReasonNotFound, "absent")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/deployments") {
			select {
			case <-time.After(80 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"demo"}}`)
			return
		}
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	var mu sync.Mutex
	deadlines := []time.Time{}
	config := &rest.Config{Host: upstream.URL, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Error("upstream call has no deadline")
			}
			mu.Lock()
			deadlines = append(deadlines, deadline)
			mu.Unlock()
			return next.RoundTrip(r)
		})
	}}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(kube.Apps{Client: client})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer api.Close()
	start := time.Now()
	response, err := http.Post(api.URL+"/apps", "application/json", strings.NewReader(`{"name":"demo","image":"nginx","containerPort":80}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 504 || *envelope.Error.Details.Resources != (kube.CreateOutcomes{Deployment: kube.Created, Service: kube.Unknown}) {
		t.Fatalf("response = %d %#v", response.StatusCode, envelope)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream request was not canceled")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline response took %v", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 4 {
		t.Fatalf("upstream calls = %d", len(deadlines))
	}
	for _, deadline := range deadlines[1:] {
		if !deadline.Equal(deadlines[0]) {
			t.Fatalf("operation deadline changed: %v", deadlines)
		}
	}
}

func TestCreateDefaultDeadlineIsTenSeconds(t *testing.T) {
	var deadlines []time.Time
	config := &rest.Config{Host: "http://controlled.invalid", WrapTransport: func(http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Fatal("upstream call missing deadline")
			}
			deadlines = append(deadlines, deadline)
			body := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
			code := 404
			if r.Method == http.MethodPost {
				body = `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"demo"}}`
				if strings.HasSuffix(r.URL.Path, "services") {
					body = `{"kind":"Service","apiVersion":"v1","metadata":{"name":"demo"}}`
				}
				code = 201
			}
			return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
	}}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(`{"name":"demo","image":"nginx","containerPort":80}`)))
	if w.Code != 202 || len(deadlines) != 4 {
		t.Fatalf("response = %d %s, calls = %d", w.Code, w.Body, len(deadlines))
	}
	for _, deadline := range deadlines {
		if !deadline.Equal(deadlines[0]) || deadline.Before(start.Add(10*time.Second)) || deadline.After(time.Now().Add(10*time.Second)) {
			t.Fatalf("incorrect operation deadline = %v", deadline)
		}
	}
}
