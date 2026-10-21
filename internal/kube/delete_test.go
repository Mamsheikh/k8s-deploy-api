package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func deleteMeta(kind string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: "demo", Namespace: Namespace, UID: types.UID(kind + "-uid"), ResourceVersion: "7", Labels: map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": "demo"}}
}

func TestDeletePresenceAndOwnership(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dep, svc bool
		change   string
		resource string
		want     ResourceOutcomes
		code     string
	}{
		{name: "absent", want: ResourceOutcomes{Absent, Absent}},
		{name: "full", dep: true, svc: true, want: ResourceOutcomes{DeleteRequested, DeleteRequested}},
		{name: "deployment only", dep: true, want: ResourceOutcomes{DeleteRequested, Absent}},
		{name: "service only", svc: true, want: ResourceOutcomes{Absent, DeleteRequested}},
		{name: "deployment deleting", dep: true, svc: true, resource: "deployments", change: "deleting", want: ResourceOutcomes{Deleting, DeleteRequested}},
		{name: "service deleting", dep: true, svc: true, resource: "services", change: "deleting", want: ResourceOutcomes{DeleteRequested, Deleting}},
		{name: "deployment collision", dep: true, svc: true, resource: "deployments", change: "owner", want: ResourceOutcomes{NotAttempted, DeleteRequested}, code: "conflict"},
		{name: "service collision", dep: true, svc: true, resource: "services", change: "name", want: ResourceOutcomes{DeleteRequested, NotAttempted}, code: "conflict"},
		{name: "deployment missing UID", dep: true, svc: true, resource: "deployments", change: "uid", want: ResourceOutcomes{NotAttempted, DeleteRequested}, code: "internal_error"},
		{name: "service missing UID", dep: true, svc: true, resource: "services", change: "uid", want: ResourceOutcomes{DeleteRequested, NotAttempted}, code: "internal_error"},
		{name: "deployment missing version", dep: true, svc: true, resource: "deployments", change: "version", want: ResourceOutcomes{NotAttempted, DeleteRequested}, code: "internal_error"},
		{name: "service missing version", dep: true, svc: true, resource: "services", change: "version", want: ResourceOutcomes{DeleteRequested, NotAttempted}, code: "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep, svc := &appsv1.Deployment{ObjectMeta: deleteMeta("dep")}, &corev1.Service{ObjectMeta: deleteMeta("svc")}
			var meta *metav1.ObjectMeta
			if tc.resource == "deployments" {
				meta = &dep.ObjectMeta
			} else {
				meta = &svc.ObjectMeta
			}
			switch tc.change {
			case "deleting":
				now := metav1.Now()
				meta.DeletionTimestamp = &now
			case "owner":
				meta.Labels["app.kubernetes.io/managed-by"] = "other"
			case "name":
				meta.Labels["app.kubernetes.io/name"] = "other"
			case "uid":
				meta.UID = ""
			case "version":
				meta.ResourceVersion = ""
			}
			objects := []runtime.Object{}
			if tc.dep {
				objects = append(objects, dep)
			}
			if tc.svc {
				objects = append(objects, svc)
			}
			client := fake.NewClientset(objects...)
			result, err := (Apps{Client: client}).Delete(context.Background(), "demo")
			got := result.Resources
			if tc.code != "" {
				var failure *AppError
				if !errors.As(err, &failure) || failure.Code != tc.code {
					t.Fatalf("error = %#v", err)
				}
				got = *failure.Details.Resources
			} else if err != nil {
				t.Fatal(err)
			} else if result.Name != "demo" || result.Namespace != Namespace || result.StatusURL != "/apps/demo" {
				t.Fatalf("result = %#v", result)
			}
			if got != tc.want {
				t.Fatalf("outcomes = %#v, want %#v", got, tc.want)
			}
			for _, action := range client.Actions() {
				if action.GetNamespace() != Namespace || (action.GetResource().Resource != "deployments" && action.GetResource().Resource != "services") || (action.GetVerb() != "get" && action.GetVerb() != "delete") {
					t.Fatalf("unexpected mutation or lookup: %v", action)
				}
				if action.GetVerb() == "delete" {
					resource := action.GetResource().Resource
					outcome := got.Deployment
					if resource == "services" {
						outcome = got.Service
					}
					if outcome != DeleteRequested {
						t.Fatalf("unsafe delete of %s with outcome %s", resource, outcome)
					}
				}
			}
			if tc.change != "" && tc.change != "deleting" {
				var getErr error
				if tc.resource == "deployments" {
					_, getErr = client.AppsV1().Deployments(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
				} else {
					_, getErr = client.CoreV1().Services(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
				}
				if getErr != nil {
					t.Fatalf("guarded resource was removed: %v", getErr)
				}
			}
		})
	}
}

func TestDeleteFailuresPreserveBothAttempts(t *testing.T) {
	conflict := apierrors.NewConflict(deploymentResource, "demo", errors.New("replacement"))
	unavailable := apierrors.NewForbidden(serviceResource, "demo", errors.New("denied"))
	timeout := apierrors.NewTimeoutError("controlled deadline", 0)
	internal := errors.New("secret transport details")
	for _, tc := range []struct {
		name, depVerb, svcVerb string
		depErr, svcErr         error
		want                   ResourceOutcomes
		code                   string
	}{
		{"deployment read failure", "get", "", unavailable, nil, ResourceOutcomes{NotAttempted, DeleteRequested}, "upstream_unavailable"},
		{"service read failure", "", "get", nil, unavailable, ResourceOutcomes{DeleteRequested, NotAttempted}, "upstream_unavailable"},
		{"deployment write failure", "delete", "", conflict, nil, ResourceOutcomes{Failed, DeleteRequested}, "conflict"},
		{"service write failure", "", "delete", nil, unavailable, ResourceOutcomes{DeleteRequested, Failed}, "upstream_unavailable"},
		{"both write failures", "delete", "delete", conflict, unavailable, ResourceOutcomes{Failed, Failed}, "upstream_unavailable"},
		{"timeout outranks unavailable", "delete", "delete", timeout, unavailable, ResourceOutcomes{Unknown, Failed}, "upstream_timeout"},
		{"unavailable outranks internal", "delete", "delete", internal, unavailable, ResourceOutcomes{Unknown, Failed}, "upstream_unavailable"},
		{"internal outranks conflict", "delete", "delete", conflict, internal, ResourceOutcomes{Failed, Unknown}, "internal_error"},
		{"GET failure then write failure", "get", "delete", timeout, conflict, ResourceOutcomes{NotAttempted, Failed}, "upstream_timeout"},
		{"both GET failures", "get", "get", conflict, unavailable, ResourceOutcomes{NotAttempted, NotAttempted}, "upstream_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset(&appsv1.Deployment{ObjectMeta: deleteMeta("dep")}, &corev1.Service{ObjectMeta: deleteMeta("svc")})
			for _, injection := range []struct {
				resource, verb string
				err            error
			}{{"deployments", tc.depVerb, tc.depErr}, {"services", tc.svcVerb, tc.svcErr}} {
				if injection.err != nil {
					client.PrependReactor(injection.verb, injection.resource, func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, injection.err })
				}
			}
			_, err := (Apps{Client: client}).Delete(context.Background(), "demo")
			var failure *AppError
			if !errors.As(err, &failure) || failure.Code != tc.code || *failure.Details.Resources != tc.want {
				t.Fatalf("error = %#v", err)
			}
			if failure.Details.Name != "demo" || failure.Details.Namespace != Namespace || failure.Details.Failures == nil {
				t.Fatalf("details = %#v", failure.Details)
			}
			for i, source := range []error{tc.depErr, tc.svcErr} {
				detail := failure.Details.Failures.Deployment
				if i == 1 {
					detail = failure.Details.Failures.Service
				}
				if source == nil {
					if detail != nil {
						t.Fatalf("unexpected failure = %#v", detail)
					}
					continue
				}
				normalized := operationError(source)
				if detail == nil || detail.Code != normalized.Code || detail.Reason != normalized.Details.Reason || detail.Message != normalized.Message {
					t.Fatalf("lost resource error: %#v", detail)
				}
			}
			if strings.Contains(failure.Message, "secret") {
				t.Fatalf("leaked error = %v", failure)
			}
			serviceRead := false
			for _, action := range client.Actions() {
				if action.GetResource().Resource == "services" && action.GetVerb() == "get" {
					serviceRead = true
				}
			}
			if !serviceRead {
				t.Fatal("second resource was never attempted")
			}
		})
	}
}

func TestDeleteNotFoundRace(t *testing.T) {
	client := fake.NewClientset(&appsv1.Deployment{ObjectMeta: deleteMeta("dep")}, &corev1.Service{ObjectMeta: deleteMeta("svc")})
	client.PrependReactor("delete", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: action.GetResource().Resource}, "demo")
	})
	result, err := (Apps{Client: client}).Delete(context.Background(), "demo")
	if err != nil || result.Resources != (ResourceOutcomes{Absent, Absent}) {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestDeleteCancellationSkipsUnissuedWrites(t *testing.T) {
	for _, stage := range []string{"before operation", "after read", "after first delete"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := fake.NewClientset(&appsv1.Deployment{ObjectMeta: deleteMeta("dep")}, &corev1.Service{ObjectMeta: deleteMeta("svc")})
			if stage == "before operation" {
				cancel()
			} else {
				verb := "get"
				if stage == "after first delete" {
					verb = "delete"
				}
				client.PrependReactor(verb, "deployments", func(clienttesting.Action) (bool, runtime.Object, error) { cancel(); return false, nil, nil })
			}
			_, err := (Apps{Client: client}).Delete(ctx, "demo")
			var failure *AppError
			want := ResourceOutcomes{NotAttempted, NotAttempted}
			if stage == "after first delete" {
				want.Deployment = DeleteRequested
			}
			if !errors.As(err, &failure) || failure.Code != "upstream_unavailable" || *failure.Details.Resources != want {
				t.Fatalf("error = %#v", err)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "delete" && (stage != "after first delete" || action.GetResource().Resource != "deployments") {
					t.Fatalf("unissued deletion reached upstream: %v", action)
				}
			}
		})
	}
}
