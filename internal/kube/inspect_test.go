package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestInspectCompleteAndPartial(t *testing.T) {
	for _, shape := range []string{"complete", "deployment-only", "service-only", "absent", "unmanaged-deployment", "unmanaged-service", "colliding-service"} {
		t.Run(shape, func(t *testing.T) {
			d, r, p := inspectionFixture()
			s := service(AppSpec{Name: "demo", ContainerPort: 8080})
			now := metav1.Now()
			d.DeletionTimestamp = &now
			s.DeletionTimestamp = &now
			d.Generation = 3
			d.Status = appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded", Message: "rollout stuck"}}}
			objects := []runtime.Object{}
			switch shape {
			case "complete":
				objects = []runtime.Object{d, s, r, p}
			case "deployment-only":
				objects = []runtime.Object{d, r, p}
			case "service-only":
				objects = []runtime.Object{s}
			case "unmanaged-deployment":
				d.Labels = nil
				objects = []runtime.Object{d}
			case "unmanaged-service":
				s.Labels = nil
				objects = []runtime.Object{s}
			case "colliding-service":
				s.Labels = nil
				objects = []runtime.Object{d, s, r, p}
			}
			client := fake.NewClientset(objects...)
			got, err := (Apps{Client: client}).Inspect(context.Background(), "demo")
			wantCode := ""
			if shape == "absent" {
				wantCode = "not_found"
			}
			if strings.Contains(shape, "unmanaged") || shape == "colliding-service" {
				wantCode = "conflict"
			}
			if wantCode != "" {
				var failure *AppError
				if !errors.As(err, &failure) || failure.Code != wantCode {
					t.Fatalf("got %#v %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Ready || got.Name != "demo" || got.Namespace != Namespace || got.Pods == nil {
				t.Fatalf("result %#v", got)
			}
			if got.Resources.Deployment != (shape != "service-only") || got.Resources.Service != (shape != "deployment-only") {
				t.Fatalf("presence %#v", got.Resources)
			}
			if shape == "service-only" {
				if got.Image != nil || got.ContainerPort != nil || got.Deployment != nil || len(got.Pods) != 0 {
					t.Fatalf("invented configuration %#v", got)
				}
			} else {
				if got.Image == nil || *got.Image != "nginx:v2" || got.ContainerPort == nil || *got.ContainerPort != 8080 || len(got.Pods) != 1 {
					t.Fatalf("configuration %#v", got)
				}
				v := got.Deployment
				if v.Generation != 3 || v.ObservedGeneration != 2 || v.DesiredReplicas != 1 || v.Replicas != 2 || v.UpdatedReplicas != 1 || v.ReadyReplicas != 1 || v.AvailableReplicas != 1 || v.DeletionTimestamp == nil || len(v.Conditions) != 1 || v.Conditions[0].Message != "rollout stuck" {
					t.Fatalf("deployment %#v", v)
				}
			}
			if got.Service != nil && got.Service.DeletionTimestamp == nil {
				t.Fatal("missing service deletion timestamp")
			}
			for _, action := range client.Actions() {
				if action.GetVerb() != "get" && action.GetVerb() != "list" {
					t.Fatal(action)
				}
			}
		})
	}
}

func TestInspectContainerStates(t *testing.T) {
	now := metav1.Now()
	for _, state := range []corev1.ContainerState{
		{}, {Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull failed"}},
		{Running: &corev1.ContainerStateRunning{StartedAt: now}},
		{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: "exited", FinishedAt: now}},
	} {
		d, r, p := inspectionFixture()
		p.DeletionTimestamp = &now
		p.Status.Phase = corev1.PodRunning
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "sidecar", Image: "ignore"}, {Name: "app", Image: "nginx:v1", ImageID: "digest", Ready: true, RestartCount: 4, State: state, LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}}}}
		got, err := (Apps{Client: fake.NewClientset(d, r, p)}).Inspect(context.Background(), "demo")
		if err != nil {
			t.Fatal(err)
		}
		v := got.Pods[0]
		c := v.Container
		if got.Ready || !v.Ready || v.DeletionTimestamp == nil || v.Phase != corev1.PodRunning || c == nil || c.Image != "nginx:v1" || c.ImageID != "digest" || !c.Ready || c.RestartCount != 4 || c.LastTerminationState.Terminated.ExitCode != 137 || c.LastTerminationState.Terminated.Reason != "OOMKilled" {
			t.Fatalf("pod %#v container %#v", v, c)
		}
		want, _ := json.Marshal(state)
		actual, _ := json.Marshal(c.State)
		if string(want) != string(actual) {
			t.Fatalf("state %s want %s", actual, want)
		}
	}
	d, r, p := inspectionFixture()
	got, err := (Apps{Client: fake.NewClientset(d, r, p)}).Inspect(context.Background(), "demo")
	if err != nil || got.Pods[0].Container != nil || got.Pods[0].Ready {
		t.Fatalf("missing status %#v %v", got, err)
	}
	d.Spec.Template.Spec.Containers = nil
	got, err = (Apps{Client: fake.NewClientset(d, r, p)}).Inspect(context.Background(), "demo")
	if err != nil || got.Image != nil || got.ContainerPort != nil {
		t.Fatalf("missing spec %#v %v", got, err)
	}
}

func TestInspectRequiredReadErrors(t *testing.T) {
	for _, resource := range []string{"deployments", "services", "replicasets", "pods"} {
		for _, kind := range []string{"forbidden", "notfound", "unexpected"} {
			t.Run(resource+"/"+kind, func(t *testing.T) {
				d, r, p := inspectionFixture()
				client := fake.NewClientset(d, r, p, service(AppSpec{Name: "demo"}))
				verb := "list"
				if resource == "deployments" || resource == "services" {
					verb = "get"
				}
				client.PrependReactor(verb, resource, func(clienttesting.Action) (bool, runtime.Object, error) {
					gr := schema.GroupResource{Resource: resource}
					switch kind {
					case "forbidden":
						return true, nil, apierrors.NewForbidden(gr, "demo", errors.New("denied"))
					case "notfound":
						return true, nil, apierrors.NewNotFound(gr, "demo")
					default:
						return true, nil, errors.New("secret transport details")
					}
				})
				got, err := (Apps{Client: client}).Inspect(context.Background(), "demo")
				if kind == "notfound" && verb == "get" {
					if err != nil || got.Ready {
						t.Fatalf("partial %#v %v", got, err)
					}
					return
				}
				var failure *AppError
				if !errors.As(err, &failure) || failure.Code == "not_found" || strings.Contains(failure.Message, "secret") {
					t.Fatalf("error %v", err)
				}
			})
		}
	}
}

func readyInspectionFixture() (*appsv1.Deployment, *corev1.Service, *appsv1.ReplicaSet, *corev1.Pod) {
	d, r, p := inspectionFixture()
	d.Generation = 3
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: 3, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	r.Spec.Template = *d.Spec.Template.DeepCopy()
	r.Spec.Template.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "hash"
	p.Spec = *d.Spec.Template.Spec.DeepCopy()
	p.Status = corev1.PodStatus{
		Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Image: "nginx:v2", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}},
	}
	return d, service(AppSpec{Name: "demo", ContainerPort: 8080}), r, p
}

func TestInspectRequestedRolloutReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.Deployment, *appsv1.ReplicaSet, *corev1.Pod)
		want   bool
	}{
		{"current template with controller hash", func(*appsv1.Deployment, *appsv1.ReplicaSet, *corev1.Pod) {}, true},
		{"old image despite complete counters", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) {
			d.Spec.Template.Spec.Containers[0].Image = "missing:v3"
		}, false},
		{"same image with new template annotation", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) {
			d.Spec.Template.Annotations = map[string]string{"restart": "new"}
		}, false},
		{"same image with new template label", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) {
			d.Spec.Template.Labels["version"] = "new"
		}, false},
		{"same image with changed port", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) {
			d.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort++
		}, false},
		{"deleting current replicaset", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) {
			now := metav1.Now()
			r.DeletionTimestamp = &now
		}, false},
		{"replacement deployment UID", func(d *appsv1.Deployment, _ *appsv1.ReplicaSet, _ *corev1.Pod) { d.UID = "replacement" }, false},
		{"replacement replicaset UID", func(_ *appsv1.Deployment, r *appsv1.ReplicaSet, _ *corev1.Pod) { r.UID = "replacement" }, false},
		{"mutated pod spec with requested status image", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) {
			p.Spec.Containers[0].Image = "other:v1"
		}, false},
		{"missing named app in pod spec", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) { p.Spec.Containers[0].Name = "sidecar" }, false},
		{"runtime resolved status image", func(_ *appsv1.Deployment, _ *appsv1.ReplicaSet, p *corev1.Pod) {
			p.Status.ContainerStatuses[0].Image = "docker.io/library/nginx:v2"
		}, true},
		{"digest runtime normalization", func(d *appsv1.Deployment, r *appsv1.ReplicaSet, p *corev1.Pod) {
			image := "docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5"
			d.Spec.Template.Spec.Containers[0].Image = image
			r.Spec.Template.Spec.Containers[0].Image = image
			p.Spec.Containers[0].Image = image
			p.Status.ContainerStatuses[0].Image = "nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5"
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, s, r, p := readyInspectionFixture()
			tc.change(d, r, p)
			client := fake.NewClientset(d, s, r, p)
			got, err := (Apps{Client: client}).Inspect(context.Background(), "demo")
			if err != nil {
				t.Fatal(err)
			}
			if got.Ready != tc.want {
				t.Fatalf("ready=%v, want %v, result %#v", got.Ready, tc.want, got)
			}
			if len(client.Actions()) != 4 {
				t.Fatalf("readiness introduced Kubernetes reads: %v", client.Actions())
			}
		})
	}
}

func TestInspectFailedReplacementAndRecovery(t *testing.T) {
	d, s, oldSet, oldPod := readyInspectionFixture()
	d.Spec.Template.Spec.Containers[0].Image = "missing:v3"
	d.Status.Replicas = 2
	d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded", Message: "new replica cannot pull"}}
	newSet, newPod := oldSet.DeepCopy(), oldPod.DeepCopy()
	newSet.Name, newSet.UID = "demo-new", "new-set"
	newSet.Spec.Template = *d.Spec.Template.DeepCopy()
	newPod.Name, newPod.UID = "demo-new-pod", "new-pod"
	newPod.OwnerReferences = controller("ReplicaSet", newSet.Name, newSet.UID)
	newPod.Spec = *newSet.Spec.Template.Spec.DeepCopy()
	newPod.Status.Conditions[0].Status = corev1.ConditionFalse
	newPod.Status.ContainerStatuses[0] = corev1.ContainerStatus{Name: "app", Image: "missing:v3", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "image does not exist"}}}
	client := fake.NewClientset(d, s, oldSet, oldPod, newSet, newPod)
	apps := Apps{Client: client}
	for _, replicas := range []int32{2, 1} {
		d.Status.Replicas = replicas
		if _, err := client.AppsV1().Deployments(Namespace).Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		got, err := apps.Inspect(context.Background(), "demo")
		if err != nil {
			t.Fatal(err)
		}
		if got.Ready || len(got.Pods) != 2 || !got.Pods[1].Ready || got.Pods[0].Container.State.Waiting.Reason != "ImagePullBackOff" || got.Deployment.Conditions[0].Reason != "ProgressDeadlineExceeded" || got.Deployment.Conditions[0].Message != "new replica cannot pull" {
			t.Fatalf("failed replacement %#v", got)
		}
	}
	if err := client.CoreV1().Pods(Namespace).Delete(context.Background(), oldPod.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	newPod.Status = *oldPod.Status.DeepCopy()
	newPod.Status.ContainerStatuses[0].Image = "missing:v3"
	if _, err := client.CoreV1().Pods(Namespace).Update(context.Background(), newPod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := apps.Inspect(context.Background(), "demo")
	if err != nil || !got.Ready || got.Deployment.Conditions[0].Reason != "ProgressDeadlineExceeded" {
		t.Fatalf("recovered result %#v error %v", got, err)
	}
}

func TestInspectDoesNotReuseRolloutEvidenceAcrossRequests(t *testing.T) {
	d, s, r, p := readyInspectionFixture()
	client := fake.NewClientset(d, s, r, p)
	apps := Apps{Client: client}
	assertReady := func(want bool) {
		t.Helper()
		got, err := apps.Inspect(context.Background(), "demo")
		if err != nil || got.Ready != want {
			t.Fatalf("ready=%v, want %v, error %v", got.Ready, want, err)
		}
	}
	assertReady(true)
	d.Generation++
	d.Status.ObservedGeneration++
	d.Spec.Template.Annotations = map[string]string{"restart": "next"}
	if _, err := client.AppsV1().Deployments(Namespace).Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	assertReady(false)
	r.Spec.Template = *d.Spec.Template.DeepCopy()
	r.UID = "replacement-set"
	if _, err := client.AppsV1().ReplicaSets(Namespace).Update(context.Background(), r, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	assertReady(false)
	p.OwnerReferences = controller("ReplicaSet", r.Name, r.UID)
	if _, err := client.CoreV1().Pods(Namespace).Update(context.Background(), p, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	assertReady(true)
	d.UID = "replacement-deployment"
	if _, err := client.AppsV1().Deployments(Namespace).Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	assertReady(false)
}
