package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s-deploy-api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestImageUpdateHTTPStrictValidation(t *testing.T) {
	bodies := []string{
		``, `{`, `null`, `[]`, `"image:v2"`, `1`, `true`, `{}`,
		`{"image":null}`, `{"image":1}`, `{"image":[]}`, `{"image":{}}`,
		`{"image":""}`, `{"image":" \t\n"}`,
		`{"image":"nginx:v2","name":"other"}`,
		`{"image":"nginx:v2","containerPort":80}`,
		`{"image":"nginx:v2","extra":true}`,
		`{"image":"nginx:v2"} {}`, `{"image":"nginx:v2"} garbage`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			client := fake.NewClientset()
			w := httptest.NewRecorder()
			NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/apps/demo/image", strings.NewReader(body)))
			var response errorEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || response.Error.Code != "invalid_request" || w.Header().Get("Location") != "" || w.Header().Get("Content-Type") != "application/json" || len(client.Actions()) != 0 {
				t.Fatalf("response %d %s, upstream actions %v", w.Code, w.Body, client.Actions())
			}
		})
	}
	for _, name := range []string{"1demo", "Demo", "-demo", "demo-", "demo.test", "demo_test", "%20demo", strings.Repeat("a", 64), "demo%2Fother"} {
		t.Run(name, func(t *testing.T) {
			client := fake.NewClientset()
			w := httptest.NewRecorder()
			NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/apps/"+name+"/image", strings.NewReader(`{"image":"nginx:v2"}`)))
			if w.Code != 400 || len(client.Actions()) != 0 {
				t.Fatalf("response %d %s, upstream actions %v", w.Code, w.Body, client.Actions())
			}
		})
	}
}

func TestImageUpdateHTTPAcceptedResult(t *testing.T) {
	for _, name := range []string{"a", "a-1", strings.Repeat("a", 63)} {
		t.Run(name, func(t *testing.T) {
			client := fake.NewClientset()
			handler := NewHandler(kube.Apps{Client: client})
			created := httptest.NewRecorder()
			handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(fmt.Sprintf(`{"name":%q,"image":"nginx:v1","containerPort":80}`, name))))
			if created.Code != 202 {
				t.Fatalf("create response %d %s", created.Code, created.Body)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/apps/"+name+"/image", strings.NewReader(`{"image":"nginx:v1"}`)))
			var result map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 202 || w.Header().Get("Location") != "/apps/"+name || w.Header().Get("Content-Type") != "application/json" || len(result) != 2 || result["name"] != name || result["statusUrl"] != "/apps/"+name {
				t.Fatalf("response %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestPartialCreateListInspectAndDeleteThroughHTTP(t *testing.T) {
	client := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: kube.Namespace}})
	client.PrependReactor("create", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
		meta := action.(clienttesting.CreateAction).GetObject().(metav1.Object)
		meta.SetUID("deployment-uid")
		meta.SetResourceVersion("7")
		return false, nil, nil
	})
	client.PrependReactor("create", "services", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "demo", errors.New("controlled Service creation denial"))
	})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	created := request(http.MethodPost, "/apps", `{"name":"demo","image":"nginx:v1","containerPort":80}`)
	var failure errorEnvelope
	if err := json.Unmarshal(created.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if created.Code != 503 || failure.Error.Details.Resources.Deployment != kube.Created || failure.Error.Details.Resources.Service != kube.Failed {
		t.Fatalf("partial create response %d %s", created.Code, created.Body)
	}
	listed := request(http.MethodGet, "/apps", "")
	if listed.Code != 200 || strings.TrimSpace(listed.Body.String()) != `{"apps":[{"name":"demo","namespace":"deploy-api","resources":{"deployment":true,"service":false}}]}` {
		t.Fatalf("partial list response %d %s", listed.Code, listed.Body)
	}
	inspected := request(http.MethodGet, "/apps/demo", "")
	var snapshot kube.InspectResult
	if err := json.Unmarshal(inspected.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if inspected.Code != 200 || !snapshot.Resources.Deployment || snapshot.Resources.Service || snapshot.Ready || snapshot.Image == nil || *snapshot.Image != "nginx:v1" {
		t.Fatalf("partial inspection response %d %s", inspected.Code, inspected.Body)
	}
	patched := request(http.MethodPatch, "/apps/demo/image", `{"image":"nginx:v2"}`)
	if patched.Code != 409 {
		t.Fatalf("partial image update response %d %s", patched.Code, patched.Body)
	}
	deleted := request(http.MethodDelete, "/apps/demo", "")
	if deleted.Code != 202 || !strings.Contains(deleted.Body.String(), `"deployment":"delete_requested","service":"absent"`) {
		t.Fatalf("partial delete response %d %s", deleted.Code, deleted.Body)
	}
	again := request(http.MethodDelete, "/apps/demo", "")
	if again.Code != 204 || again.Body.Len() != 0 {
		t.Fatalf("repeated delete response %d %s", again.Code, again.Body)
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), kube.Namespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace did not survive cleanup: %v", err)
	}
}
