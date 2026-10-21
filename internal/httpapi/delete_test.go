package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s-deploy-api/internal/kube"
)

type deleterFunc func(context.Context, string) (kube.DeleteResult, error)

func (f deleterFunc) Delete(ctx context.Context, name string) (kube.DeleteResult, error) {
	return f(ctx, name)
}
func (f deleterFunc) Create(context.Context, kube.AppSpec) (kube.CreateResult, error) {
	return kube.CreateResult{}, nil
}
func (f deleterFunc) List(context.Context) (kube.ListResult, error) { return kube.ListResult{}, nil }

func TestDeleteHTTPValidation(t *testing.T) {
	handler := NewHandler(deleterFunc(func(context.Context, string) (kube.DeleteResult, error) {
		t.Fatal("invalid name reached Kubernetes")
		return kube.DeleteResult{}, nil
	}))
	for _, name := range []string{"1demo", "Demo", "-demo", "demo-", "demo.test", "demo_test", "%20demo", strings.Repeat("a", 64), "demo%2Fother"} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/"+name, nil))
			var body errorEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || body.Error.Code != "invalid_request" || w.Header().Get("Location") != "" {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestDeleteHTTPSuccess(t *testing.T) {
	for _, outcomes := range []kube.ResourceOutcomes{{Deployment: kube.Absent, Service: kube.Absent}, {Deployment: kube.DeleteRequested, Service: kube.DeleteRequested}, {Deployment: kube.Absent, Service: kube.DeleteRequested}, {Deployment: kube.Deleting, Service: kube.Absent}} {
		for _, name := range []string{"a", "a-1", strings.Repeat("a", 63)} {
			t.Run(string(outcomes.Deployment)+string(outcomes.Service)+name, func(t *testing.T) {
				handler := NewHandler(deleterFunc(func(_ context.Context, got string) (kube.DeleteResult, error) {
					if got != name {
						t.Fatalf("name = %q", got)
					}
					return kube.DeleteResult{Name: name, Namespace: kube.Namespace, Resources: outcomes, StatusURL: "/apps/" + name}, nil
				}))
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/"+name, nil))
				if outcomes.Deployment == kube.Absent && outcomes.Service == kube.Absent {
					if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" || w.Header().Get("Location") != "" {
						t.Fatalf("response = %d %s headers %v", w.Code, w.Body, w.Header())
					}
					return
				}
				var result kube.DeleteResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Code != 202 || w.Header().Get("Location") != "/apps/"+name || result.Resources != outcomes || result.Name != name || result.Namespace != kube.Namespace || result.StatusURL != "/apps/"+name {
					t.Fatalf("response = %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

func TestDeleteHTTPErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"conflict", 409}, {"upstream_unavailable", 503}, {"upstream_timeout", 504}, {"internal_error", 500}} {
		t.Run(tc.code, func(t *testing.T) {
			handler := NewHandler(deleterFunc(func(context.Context, string) (kube.DeleteResult, error) {
				return kube.DeleteResult{}, &kube.AppError{Code: tc.code, Message: "controlled failure", Details: kube.ErrorDetails{Name: "demo", Namespace: kube.Namespace, Resources: &kube.ResourceOutcomes{Deployment: kube.Failed, Service: kube.DeleteRequested}, Failures: &kube.ResourceFailures{Deployment: &kube.ResourceFailure{Code: tc.code, Message: "controlled failure", Reason: "Controlled"}}, Reason: "Controlled"}}
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/demo", nil))
			var envelope errorEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || w.Header().Get("Location") != "" || envelope.Error.Details.Failures.Deployment.Reason != "Controlled" {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
		})
	}
	handler := NewHandler(deleterFunc(func(context.Context, string) (kube.DeleteResult, error) {
		return kube.DeleteResult{}, errors.New("https://user:secret@private.invalid")
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/apps/demo", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
}
