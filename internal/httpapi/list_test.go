package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"k8s-deploy-api/internal/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestListHTTP(t *testing.T) {
	meta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: kube.Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": name}}
	}
	client := fake.NewClientset(
		&corev1.Service{ObjectMeta: meta("z-service")},
		&appsv1.Deployment{ObjectMeta: meta("complete")},
		&corev1.Service{ObjectMeta: meta("complete")},
		&appsv1.Deployment{ObjectMeta: meta("a-deployment")},
	)
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps", nil))
	var result kube.ListResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want := kube.ListResult{Apps: []kube.AppSummary{
		{Name: "a-deployment", Namespace: kube.Namespace, Resources: kube.AppPresence{Deployment: true}},
		{Name: "complete", Namespace: kube.Namespace, Resources: kube.AppPresence{Deployment: true, Service: true}},
		{Name: "z-service", Namespace: kube.Namespace, Resources: kube.AppPresence{Service: true}},
	}}
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || !reflect.DeepEqual(result, want) {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"deployment":false,"service":true`) {
		t.Fatalf("missing explicit absence = %s", w.Body)
	}
	empty := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: fake.NewClientset()}).ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/apps", nil))
	if empty.Code != 200 || strings.TrimSpace(empty.Body.String()) != `{"apps":[]}` {
		t.Fatalf("empty = %d %s", empty.Code, empty.Body)
	}
}

func TestListRetainedDeploymentAfterHandlerReconstruction(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("create", "services", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "retained", errors.New("write denied"))
	})
	created := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(`{"name":"retained","image":"nginx","containerPort":80}`)))
	if created.Code != 503 {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	listed := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: client}).ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/apps", nil))
	if listed.Code != 200 || strings.TrimSpace(listed.Body.String()) != `{"apps":[{"name":"retained","namespace":"deploy-api","resources":{"deployment":true,"service":false}}]}` {
		t.Fatalf("list = %d %s", listed.Code, listed.Body)
	}
}
