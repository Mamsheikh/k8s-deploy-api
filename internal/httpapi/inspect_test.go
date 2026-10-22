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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func (f inspectorFunc) UpdateImage(context.Context, string, kube.ImageUpdateRequest) (kube.ImageUpdateResult, error) {
	return kube.ImageUpdateResult{}, errors.New("unexpected image update")
}

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

func TestInspectHTTPRequestedRollout(t *testing.T) {
	for _, shape := range []string{"healthy", "unobserved", "failed-replacement", "same-image-replacement", "recovered", "partial", "deleting"} {
		t.Run(shape, func(t *testing.T) {
			labels := map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": "demo"}
			meta := metav1.ObjectMeta{Name: "demo", Namespace: kube.Namespace, UID: "deployment-uid", Labels: labels, Generation: 3}
			one, yes := int32(1), true
			d := &appsv1.Deployment{ObjectMeta: meta, Spec: appsv1.DeploymentSpec{Replicas: &one, Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:v2", Ports: []corev1.ContainerPort{{ContainerPort: 80}}}}},
			}}, Status: appsv1.DeploymentStatus{ObservedGeneration: 3, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}}
			s := &corev1.Service{ObjectMeta: meta}
			r := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "demo-rs", Namespace: kube.Namespace, UID: "set-uid", Labels: labels,
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "demo", UID: d.UID, Controller: &yes}}},
				Spec: appsv1.ReplicaSetSpec{Template: *d.Spec.Template.DeepCopy()}}
			p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "demo-pod", Namespace: kube.Namespace, UID: "pod-uid", Labels: labels,
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: r.Name, UID: r.UID, Controller: &yes}}},
				Spec: *d.Spec.Template.Spec.DeepCopy(), Status: corev1.PodStatus{Phase: corev1.PodRunning,
					Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
					ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Image: "docker.io/library/nginx:v2", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
			switch shape {
			case "unobserved":
				d.Status.ObservedGeneration--
			case "failed-replacement":
				d.Spec.Template.Spec.Containers[0].Image = "missing:v3"
				fallthrough
			case "recovered":
				d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded", Message: "retained upstream diagnostic"}}
			case "same-image-replacement":
				d.Spec.Template.Annotations = map[string]string{"restart": "next"}
			case "deleting":
				now := metav1.Now()
				s.DeletionTimestamp = &now
			}
			objects := []runtime.Object{d, r, p}
			if shape != "partial" {
				objects = append(objects, s)
			}
			w := httptest.NewRecorder()
			NewHandler(kube.Apps{Client: fake.NewClientset(objects...)}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/demo", nil))
			var got kube.InspectResult
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			wantReady := shape == "healthy" || shape == "recovered"
			if w.Code != 200 || got.Ready != wantReady || len(got.Pods) != 1 || got.Pods[0].Container.Image != "docker.io/library/nginx:v2" {
				t.Fatalf("response %d %s", w.Code, w.Body)
			}
			if shape == "failed-replacement" || shape == "recovered" {
				condition := got.Deployment.Conditions[0]
				if condition.Type != appsv1.DeploymentProgressing || condition.Status != corev1.ConditionFalse || condition.Reason != "ProgressDeadlineExceeded" || condition.Message != "retained upstream diagnostic" {
					t.Fatalf("condition changed %#v", condition)
				}
			}
			var raw struct {
				Pods []map[string]json.RawMessage `json:"pods"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if len(raw.Pods[0]) != 4 {
				t.Fatalf("Pod JSON changed %s", w.Body)
			}
			for _, key := range []string{"name", "phase", "ready", "container"} {
				if _, ok := raw.Pods[0][key]; !ok {
					t.Fatalf("missing Pod JSON field %s", key)
				}
			}
		})
	}
}
