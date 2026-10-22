package kube

import (
	"context"
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func imageUpdateFixture() (*appsv1.Deployment, *corev1.Service) {
	spec := AppSpec{Name: "demo", Image: "example.test/app:v1", ContainerPort: 8080}
	d, s := deployment(spec), service(spec)
	d.UID, d.ResourceVersion, d.Generation = "deployment-uid", "7", 3
	d.Annotations = map[string]string{"retained": "deployment annotation"}
	d.Finalizers = []string{"example.test/retained"}
	d.Spec.Template.Annotations = map[string]string{"retained": "pod annotation"}
	d.Spec.Template.Spec.NodeSelector = map[string]string{"example.test/node": "local"}
	d.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "RETAINED", Value: "configuration"}}
	d.Spec.Template.Spec.Containers = append([]corev1.Container{{Name: "sidecar", Image: "example.test/sidecar:v1"}}, d.Spec.Template.Spec.Containers...)
	s.UID, s.ResourceVersion, s.Spec.ClusterIP = "service-uid", "8", "10.96.0.10"
	s.Spec.ClusterIPs = []string{s.Spec.ClusterIP}
	s.Annotations = map[string]string{"retained": "service annotation"}
	return d, s
}

func TestImageUpdateChangesOnlyNamedContainerImage(t *testing.T) {
	d, s := imageUpdateFixture()
	client := fake.NewClientset(d, s)
	want := d.DeepCopy()
	want.Spec.Template.Spec.Containers[1].Image = "example.test/app:v2"
	got, err := (Apps{Client: client}).UpdateImage(context.Background(), "demo", ImageUpdateRequest{Image: "example.test/app:v2"})
	if err != nil || got != (ImageUpdateResult{Name: "demo", StatusURL: "/apps/demo"}) {
		t.Fatalf("result %#v, error %v", got, err)
	}
	stored, err := client.AppsV1().Deployments(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(stored, want) {
		t.Fatalf("Deployment changed beyond image: %#v, error %v", stored, err)
	}
	storedService, err := client.CoreV1().Services(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(storedService, s) {
		t.Fatalf("Service changed: %#v, error %v", storedService, err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" && (action.GetVerb() != "update" || action.GetResource().Resource != "deployments") {
			t.Fatalf("unexpected mutation %v", action)
		}
	}
}

func TestImageUpdateIdenticalReferenceDoesNotWrite(t *testing.T) {
	d, s := imageUpdateFixture()
	client := fake.NewClientset(d, s)
	got, err := (Apps{Client: client}).UpdateImage(context.Background(), "demo", ImageUpdateRequest{Image: "example.test/app:v1"})
	if err != nil || got != (ImageUpdateResult{Name: "demo", StatusURL: "/apps/demo"}) {
		t.Fatalf("result %#v, error %v", got, err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" {
			t.Fatalf("identical reference wrote %v", action)
		}
	}
	stored, err := client.AppsV1().Deployments(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(stored, d) {
		t.Fatalf("no-op changed Deployment: %#v, error %v", stored, err)
	}
}

func TestImageUpdateRejectsMissingPartialDeletingAndUnmanagedApps(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{"absent", "not_found"},
		{"deployment only", "conflict"},
		{"service only", "conflict"},
		{"deployment deleting", "conflict"},
		{"service deleting", "conflict"},
		{"deployment unmanaged", "conflict"},
		{"service unmanaged", "conflict"},
		{"deployment wrong app label", "conflict"},
		{"service wrong app label", "conflict"},
		{"no app container", "conflict"},
		{"missing UID", "internal_error"},
		{"missing resourceVersion", "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, s := imageUpdateFixture()
			objects := []runtime.Object{d, s}
			now := metav1.Now()
			switch tc.name {
			case "absent":
				objects = nil
			case "deployment only":
				objects = []runtime.Object{d}
			case "service only":
				objects = []runtime.Object{s}
			case "deployment deleting":
				d.DeletionTimestamp = &now
			case "service deleting":
				s.DeletionTimestamp = &now
			case "deployment unmanaged":
				d.Labels = nil
			case "service unmanaged":
				s.Labels = nil
			case "deployment wrong app label":
				d.Labels["app.kubernetes.io/name"] = "other"
			case "service wrong app label":
				s.Labels["app.kubernetes.io/name"] = "other"
			case "no app container":
				d.Spec.Template.Spec.Containers = d.Spec.Template.Spec.Containers[:1]
			case "missing UID":
				d.UID = ""
			case "missing resourceVersion":
				d.ResourceVersion = ""
			}
			client := fake.NewClientset(objects...)
			_, err := (Apps{Client: client}).UpdateImage(context.Background(), "demo", ImageUpdateRequest{Image: "example.test/app:v2"})
			var failure *AppError
			if !errors.As(err, &failure) || failure.Code != tc.code || failure.Details.Name != "demo" || failure.Details.Namespace != Namespace {
				t.Fatalf("error %#v", err)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() != "get" {
					t.Fatalf("rejected update wrote %v", action)
				}
			}
		})
	}
}

func TestImageUpdateCancellationPreventsUnissuedWrites(t *testing.T) {
	for _, afterRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "before reads", true: "after reads"}[afterRead], func(t *testing.T) {
			d, s := imageUpdateFixture()
			client := fake.NewClientset(d, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if afterRead {
				client.PrependReactor("get", "services", func(clienttesting.Action) (bool, runtime.Object, error) {
					cancel()
					return false, nil, nil
				})
			} else {
				cancel()
			}
			_, err := (Apps{Client: client}).UpdateImage(ctx, "demo", ImageUpdateRequest{Image: "example.test/app:v2"})
			var failure *AppError
			if !errors.As(err, &failure) || failure.Code != "upstream_unavailable" {
				t.Fatalf("error %#v", err)
			}
			for _, action := range client.Actions() {
				if !afterRead || action.GetVerb() != "get" {
					t.Fatalf("canceled operation issued %v", action)
				}
			}
		})
	}
}

func TestImageUpdateOldHealthyAndNewUnready(t *testing.T) {
	d, s, oldSet, oldPod := readyInspectionFixture()
	d.ResourceVersion = "7"
	client := fake.NewClientset(d, s, oldSet, oldPod)
	apps := Apps{Client: client}
	if _, err := apps.UpdateImage(context.Background(), "demo", ImageUpdateRequest{Image: "example.test/missing:v2"}); err != nil {
		t.Fatal(err)
	}
	updated, err := client.AppsV1().Deployments(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newSet, newPod := oldSet.DeepCopy(), oldPod.DeepCopy()
	newSet.Name, newSet.UID = "demo-new", "new-set"
	newSet.Spec.Template = *updated.Spec.Template.DeepCopy()
	newPod.Name, newPod.UID = "demo-new-pod", "new-pod"
	newPod.OwnerReferences = controller("ReplicaSet", newSet.Name, newSet.UID)
	newPod.Spec = *newSet.Spec.Template.Spec.DeepCopy()
	newPod.Status.Phase = corev1.PodPending
	newPod.Status.Conditions[0].Status = corev1.ConditionFalse
	newPod.Status.ContainerStatuses[0] = corev1.ContainerStatus{Name: "app", Image: "example.test/missing:v2", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "new image cannot be pulled"}}}
	if _, err := client.AppsV1().ReplicaSets(Namespace).Create(context.Background(), newSet, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods(Namespace).Create(context.Background(), newPod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := apps.Inspect(context.Background(), "demo")
	if err != nil || got.Ready || got.Image == nil || *got.Image != "example.test/missing:v2" || len(got.Pods) != 2 {
		t.Fatalf("inspection %#v, error %v", got, err)
	}
	if got.Pods[0].Container.State.Waiting.Reason != "ImagePullBackOff" || !got.Pods[1].Ready || got.Pods[1].Container.Image != "nginx:v2" {
		t.Fatalf("old and replacement Pod diagnostics %#v", got.Pods)
	}
	t.Run("recovery", func(t *testing.T) {
		if _, err := apps.UpdateImage(context.Background(), "demo", ImageUpdateRequest{Image: "nginx:v2"}); err != nil {
			t.Fatal(err)
		}
		if err := client.CoreV1().Pods(Namespace).Delete(context.Background(), newPod.Name, metav1.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		recovered, err := apps.Inspect(context.Background(), "demo")
		if err != nil || !recovered.Ready || recovered.Image == nil || *recovered.Image != "nginx:v2" {
			t.Fatalf("recovery inspection %#v, error %v", recovered, err)
		}
	})
}
