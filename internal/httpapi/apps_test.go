package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s-deploy-api/internal/kube"
	"k8s.io/client-go/kubernetes/fake"
)

type creatorFunc func(context.Context, kube.AppSpec) (kube.CreateResult, error)

func (f creatorFunc) Create(ctx context.Context, spec kube.AppSpec) (kube.CreateResult, error) {
	return f(ctx, spec)
}

func (f creatorFunc) List(context.Context) (kube.ListResult, error) {
	return kube.ListResult{Apps: []kube.AppSummary{}}, nil
}

func TestCreateValidation(t *testing.T) {
	cases := []string{
		``, `{`, `null`, `[]`, `"text"`, `1`, `true`,
		`{"name":"demo","image":"nginx","containerPort":80,"extra":true}`,
		`{"name":"demo","image":"nginx","containerPort":80} {}`,
		`{"name":"demo","image":"nginx","containerPort":80} garbage`,
		`{"name":"demo","image":"nginx","containerPort":80.0}`,
		`{"name":"demo","image":"nginx","containerPort":"80"}`,
		`{"name":"demo","image":"nginx","containerPort":null}`,
		`{"name":"demo","image":null,"containerPort":80}`,
		`{"name":null,"image":"nginx","containerPort":80}`,
		`{"name":"demo","image":"nginx"}`, `{"name":"demo","containerPort":80}`, `{"image":"nginx","containerPort":80}`,
	}
	for _, name := range []string{"", "1demo", "Demo", "-demo", "demo-", "demo.test", "demo_test", " demo", strings.Repeat("a", 64)} {
		cases = append(cases, fmt.Sprintf(`{"name":%q,"image":"nginx","containerPort":80}`, name))
	}
	for _, port := range []int{0, -1, 65536, 2147483647} {
		cases = append(cases, fmt.Sprintf(`{"name":"demo","image":"nginx","containerPort":%d}`, port))
	}
	for _, image := range []string{"", " \t\n"} {
		cases = append(cases, fmt.Sprintf(`{"name":"demo","image":%q,"containerPort":80}`, image))
	}
	creator := creatorFunc(func(context.Context, kube.AppSpec) (kube.CreateResult, error) {
		t.Fatal("invalid body reached Kubernetes")
		return kube.CreateResult{}, nil
	})
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			NewHandler(creator).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body)))
			var response errorEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || response.Error.Code != "invalid_request" || w.Header().Get("Location") != "" || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestCreateSuccessAndDuplicate(t *testing.T) {
	client := fake.NewClientset()
	handler := NewHandler(kube.Apps{Client: client})
	for _, name := range []string{"a", "a-1", strings.Repeat("a", 63)} {
		for _, port := range []int{1, 65535} {
			body := fmt.Sprintf(`{"name":%q,"image":"nginx:v1","containerPort":%d}`, name, port)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body)))
			if port == 65535 {
				if w.Code != 409 {
					t.Fatalf("duplicate = %d %s", w.Code, w.Body)
				}
				continue
			}
			var result kube.CreateResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 202 || w.Header().Get("Location") != "/apps/"+name || result.Name != name || result.Namespace != kube.Namespace || result.Deployment != name || result.Service != name || result.StatusURL != "/apps/"+name {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
		}
	}
}

func TestCreateValidBoundaryPorts(t *testing.T) {
	for _, port := range []int32{1, 65535} {
		handler := NewHandler(creatorFunc(func(_ context.Context, spec kube.AppSpec) (kube.CreateResult, error) {
			if spec.ContainerPort != port {
				t.Fatalf("port = %d", spec.ContainerPort)
			}
			return kube.CreateResult{Name: spec.Name, StatusURL: "/apps/" + spec.Name}, nil
		}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(fmt.Sprintf(`{"name":"demo","image":"nginx","containerPort":%d}`, port))))
		if w.Code != 202 {
			t.Fatalf("response = %d %s", w.Code, w.Body)
		}
	}
}

func TestCreateErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"invalid_request", 400}, {"not_found", 404}, {"conflict", 409}, {"upstream_unavailable", 503}, {"upstream_timeout", 504}, {"internal_error", 500}} {
		t.Run(tc.code, func(t *testing.T) {
			handler := NewHandler(creatorFunc(func(context.Context, kube.AppSpec) (kube.CreateResult, error) {
				return kube.CreateResult{}, &kube.AppError{Code: tc.code, Message: "useful failure", Details: kube.ErrorDetails{Reason: "TestReason", Resources: &kube.ResourceOutcomes{Deployment: kube.Created, Service: kube.Failed}}}
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(`{"name":"demo","image":"nginx","containerPort":80}`)))
			if w.Code != tc.status || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), `"reason":"TestReason"`) {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
		})
	}
	w := httptest.NewRecorder()
	writeError(w, errors.New("https://user:secret@cluster"))
	body, _ := io.ReadAll(w.Result().Body)
	if w.Code != 500 || strings.Contains(string(body), "secret") {
		t.Fatalf("response = %d %s", w.Code, body)
	}
}

func (f creatorFunc) Delete(context.Context, string) (kube.DeleteResult, error) {
	return kube.DeleteResult{}, nil
}

func (f creatorFunc) Inspect(context.Context, string) (kube.InspectResult, error) {
	return kube.InspectResult{}, nil
}

func (f creatorFunc) UpdateImage(context.Context, string, kube.ImageUpdateRequest) (kube.ImageUpdateResult, error) {
	return kube.ImageUpdateResult{}, errors.New("unexpected image update")
}
