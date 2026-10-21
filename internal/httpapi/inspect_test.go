package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s-deploy-api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type inspectorFunc func(context.Context, string) (kube.InspectResult, error)

func (f inspectorFunc) Inspect(ctx context.Context, name string) (kube.InspectResult, error) {
	return f(ctx, name)
}
func (f inspectorFunc) Create(context.Context, kube.AppSpec) (kube.CreateResult, error) {
	return kube.CreateResult{}, nil
}
func (f inspectorFunc) Delete(context.Context, string) (kube.DeleteResult, error) {
	return kube.DeleteResult{}, nil
}
func (f inspectorFunc) List(context.Context) (kube.ListResult, error) { return kube.ListResult{}, nil }

func TestInspectHTTPValidation(t *testing.T) {
	handler := NewHandler(inspectorFunc(func(context.Context, string) (kube.InspectResult, error) {
		t.Fatal("invalid name reached Kubernetes")
		return kube.InspectResult{}, nil
	}))
	for _, name := range []string{"Invalid", "1app", "app-", "a.b", strings.Repeat("a", 64)} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/"+name, nil))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("response %d %s", w.Code, w.Body)
		}
	}
}

func TestInspectHTTPReconstructionAndPartial(t *testing.T) {
	client := fake.NewClientset()
	for _, name := range []string{"a", "partial"} {
		if _, err := (kube.Apps{Client: client}).Create(context.Background(), kube.AppSpec{Name: name, Image: "nginx:v1", ContainerPort: 80}); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.CoreV1().Services(kube.Namespace).Delete(context.Background(), "partial", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "partial", "absent"} {
		w := httptest.NewRecorder()
		NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/"+name, nil))
		if name == "absent" {
			if w.Code != 404 {
				t.Fatalf("missing %d %s", w.Code, w.Body)
			}
			continue
		}
		var got kube.InspectResult
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || got.Name != name || got.Ready || !got.Resources.Deployment || got.Resources.Service != (name == "a") || got.Image == nil || *got.Image != "nginx:v1" || got.Pods == nil {
			t.Fatalf("response %d %s", w.Code, w.Body)
		}
	}
}

func TestInspectHTTPErrorMapping(t *testing.T) {
	for code, want := range map[string]int{"not_found": 404, "conflict": 409, "upstream_unavailable": 503, "upstream_timeout": 504, "internal_error": 500} {
		handler := NewHandler(inspectorFunc(func(ctx context.Context, name string) (kube.InspectResult, error) {
			if name != "demo" {
				t.Fatal(name)
			}
			return kube.InspectResult{}, &kube.AppError{Code: code, Message: "controlled failure"}
		}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/demo", nil))
		if w.Code != want || !strings.Contains(w.Body.String(), `"message":"controlled failure"`) {
			t.Fatalf("response %d %s", w.Code, w.Body)
		}
	}
}
