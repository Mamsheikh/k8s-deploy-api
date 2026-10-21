package kube

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestListManagedUnion(t *testing.T) {
	now := metav1.Now()
	complete := deployment(AppSpec{Name: "middle"})
	complete.DeletionTimestamp = &now
	deleting := service(AppSpec{Name: "deleting"})
	deleting.DeletionTimestamp = &now
	objects := []runtime.Object{
		service(AppSpec{Name: "z-service"}), complete, service(AppSpec{Name: "middle"}),
		deployment(AppSpec{Name: "a-deployment"}), deleting,
		deployment(AppSpec{Name: "managed-deployment"}),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "managed-deployment", Namespace: Namespace}},
		service(AppSpec{Name: "managed-service"}),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "managed-service", Namespace: Namespace}},
	}
	for _, kind := range []string{"deployment", "service"} {
		for _, invalid := range []string{"missing-managed", "wrong-managed", "missing-name", "wrong-name", "other-namespace"} {
			name := kind + "-" + invalid
			meta := metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: appLabels(name)}
			switch invalid {
			case "missing-managed":
				delete(meta.Labels, "app.kubernetes.io/managed-by")
			case "wrong-managed":
				meta.Labels["app.kubernetes.io/managed-by"] = "someone-else"
			case "missing-name":
				delete(meta.Labels, "app.kubernetes.io/name")
			case "wrong-name":
				meta.Labels["app.kubernetes.io/name"] = "middle"
			case "other-namespace":
				meta.Namespace = "other"
			}
			if kind == "deployment" {
				objects = append(objects, &appsv1.Deployment{ObjectMeta: meta})
			} else {
				objects = append(objects, &corev1.Service{ObjectMeta: meta})
			}
		}
	}
	client := fake.NewClientset(objects...)
	result, err := (Apps{Client: client}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := ListResult{Apps: []AppSummary{
		{"a-deployment", Namespace, AppPresence{Deployment: true}},
		{"deleting", Namespace, AppPresence{Service: true}},
		{"managed-deployment", Namespace, AppPresence{Deployment: true}},
		{"managed-service", Namespace, AppPresence{Service: true}},
		{"middle", Namespace, AppPresence{Deployment: true, Service: true}},
		{"z-service", Namespace, AppPresence{Service: true}},
	}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("actions = %v", actions)
	}
	for _, action := range actions {
		list, ok := action.(clienttesting.ListAction)
		if !ok || action.GetNamespace() != Namespace {
			t.Fatalf("unexpected action = %v", action)
		}
		selector := list.GetListRestrictions().Labels
		if !selector.Matches(labels.Set(appLabels("demo"))) || selector.Matches(labels.Set{"app.kubernetes.io/managed-by": "deploy-api"}) || selector.Matches(labels.Set{"app.kubernetes.io/managed-by": "other", "app.kubernetes.io/name": "demo"}) {
			t.Fatalf("ownership selector = %v", selector)
		}
	}
}

func TestListEmptyArray(t *testing.T) {
	for _, objects := range [][]runtime.Object{nil, {&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "unmanaged", Namespace: Namespace}}}} {
		result, err := (Apps{Client: fake.NewClientset(objects...)}).List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"apps":[]}` {
			t.Fatalf("result = %s", body)
		}
	}
}

func TestListFailureHasNoPartialSuccess(t *testing.T) {
	for _, resource := range []string{"deployments", "services"} {
		t.Run(resource, func(t *testing.T) {
			client := fake.NewClientset(deployment(AppSpec{Name: "retained"}))
			client.PrependReactor("list", resource, func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(serviceResource, "", errors.New("read denied"))
			})
			result, err := (Apps{Client: client}).List(context.Background())
			var failure *AppError
			if !errors.As(err, &failure) || failure.Code != "upstream_unavailable" || failure.Details.Resources != nil || result.Apps != nil {
				t.Fatalf("result = %#v, error = %#v", result, err)
			}
		})
	}
}

func TestListCancellationNeverReturnsInventory(t *testing.T) {
	for _, stage := range []string{"before deployment", "after deployment", "after service"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := fake.NewClientset(deployment(AppSpec{Name: "demo"}))
			if stage == "before deployment" {
				cancel()
			} else {
				resource := "deployments"
				if stage == "after service" {
					resource = "services"
				}
				client.PrependReactor("list", resource, func(clienttesting.Action) (bool, runtime.Object, error) { cancel(); return false, nil, nil })
			}
			result, err := (Apps{Client: client}).List(ctx)
			if err == nil || result.Apps != nil {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
			wantCalls := map[string]int{"before deployment": 0, "after deployment": 1, "after service": 2}[stage]
			if len(client.Actions()) != wantCalls {
				t.Fatalf("canceled list made %d calls, want %d", len(client.Actions()), wantCalls)
			}
		})
	}
}
