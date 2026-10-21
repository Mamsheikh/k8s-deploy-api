package kube

import (
	"context"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func controller(kind, name string, uid types.UID) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: kind, Name: name, UID: uid, Controller: &yes}}
}

func inspectionFixture() (*appsv1.Deployment, *appsv1.ReplicaSet, *corev1.Pod) {
	d := deployment(AppSpec{Name: "demo", Image: "nginx:v2", ContainerPort: 8080})
	d.UID = "deployment-uid"
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "demo-rs", Namespace: Namespace, UID: "rs-uid", Labels: appLabels("demo"), OwnerReferences: controller("Deployment", "demo", d.UID)}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "demo-pod", Namespace: Namespace, UID: "pod-uid", Labels: appLabels("demo"), OwnerReferences: controller("ReplicaSet", rs.Name, rs.UID)}}
	return d, rs, pod
}

func TestOwnedPodsRequiresLabelsAndControllerUIDChain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.Deployment, *appsv1.ReplicaSet, *corev1.Pod)
		want   bool
	}{
		{"owned", func(*appsv1.Deployment, *appsv1.ReplicaSet, *corev1.Pod) {}, true},
		{"terminating owned", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) {
			now := metav1.Now()
			p.DeletionTimestamp = &now
		}, true},
		{"deployment replaced", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) { d.UID = "new-deployment" }, false},
		{"replicaset replaced", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) { r.UID = "new-rs" }, false},
		{"no deployment uid", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) { d.UID = "" }, false},
		{"no replicasets uid", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) { r.UID = "" }, false},
		{"pod without owner", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) { p.OwnerReferences = nil }, false},
		{"set without owner", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) { r.OwnerReferences = nil }, false},
		{"pod wrong owner name", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) { p.OwnerReferences[0].Name = "other" }, false},
		{"pod wrong owner kind", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) {
			p.OwnerReferences[0].Kind = "Deployment"
		}, false},
		{"set wrong owner version", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) {
			r.OwnerReferences[0].APIVersion = "other/v1"
		}, false},
		{"pod noncontroller", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) {
			*p.OwnerReferences[0].Controller = false
		}, false},
		{"set noncontroller", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) { r.OwnerReferences[0].Controller = nil }, false},
		{"pod wrong app", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) {
			p.Labels["app.kubernetes.io/name"] = "other"
		}, false},
		{"set unmanaged", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) {
			delete(r.Labels, "app.kubernetes.io/managed-by")
		}, false},
		{"pod other namespace", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) { p.Namespace = "other" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r, p := inspectionFixture()
			tc.change(d, r, p)
			client := fake.NewClientset(d, r, p)
			got, err := (Apps{Client: client}).ownedPods(context.Background(), d)
			if err != nil || (len(got) == 1) != tc.want {
				t.Fatalf("pods=%v err=%v", got, err)
			}
			for _, a := range client.Actions() {
				if a.GetVerb() != "list" || a.GetNamespace() != Namespace {
					t.Fatalf("unexpected action %v", a)
				}
			}
		})
	}
}

func TestOwnedPodsSortedAcrossReplicaSets(t *testing.T) {
	d, r, p := inspectionFixture()
	r2, p2 := r.DeepCopy(), p.DeepCopy()
	r2.Name = "demo-rs-2"
	r2.UID = "rs-2"
	p2.Name = "a-pod"
	p2.OwnerReferences = controller("ReplicaSet", r2.Name, r2.UID)
	objects := []runtime.Object{d, r, p, r2, p2}
	got, err := (Apps{Client: fake.NewClientset(objects...)}).ownedPods(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, p := range got {
		names = append(names, p.Name)
	}
	if !reflect.DeepEqual(names, []string{"a-pod", "demo-pod"}) {
		t.Fatal(names)
	}
}
