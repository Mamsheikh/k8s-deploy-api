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
