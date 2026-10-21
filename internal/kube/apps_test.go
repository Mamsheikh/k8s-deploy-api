package kube

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestCreateResourceIntent(t *testing.T) {
	client := fake.NewClientset()
	spec := AppSpec{Name: "learn-a", Image: "example.test/app:v1", ContainerPort: 8080}
	result, err := (Apps{Client: client}).Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result != (CreateResult{Name: spec.Name, Namespace: Namespace, Deployment: spec.Name, Service: spec.Name, StatusURL: "/apps/learn-a"}) {
		t.Fatalf("result = %#v", result)
	}
	dep, err := client.AppsV1().Deployments(Namespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := client.CoreV1().Services(Namespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": "learn-a"}
	for _, got := range []map[string]string{dep.Labels, dep.Spec.Selector.MatchLabels, dep.Spec.Template.Labels, svc.Labels, svc.Spec.Selector} {
		if !reflect.DeepEqual(got, labels) {
			t.Errorf("labels/selectors = %v", got)
		}
	}
	if dep.Namespace != Namespace || svc.Namespace != Namespace || dep.Name != spec.Name || svc.Name != spec.Name {
		t.Fatal("incorrect resource identity")
	}
	if *dep.Spec.Replicas != 1 || *dep.Spec.ProgressDeadlineSeconds != 120 || dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType || *dep.Spec.Strategy.RollingUpdate.MaxSurge != intstr.FromInt32(1) || *dep.Spec.Strategy.RollingUpdate.MaxUnavailable != intstr.FromInt32(0) {
		t.Fatalf("deployment intent = %#v", dep.Spec)
	}
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatal("expected one container")
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if container.Name != "app" || container.Image != spec.Image || container.ImagePullPolicy != corev1.PullAlways || len(container.Ports) != 1 || container.Ports[0].ContainerPort != 8080 || container.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("container = %#v", container)
	}
	probe := container.ReadinessProbe
	if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port != intstr.FromInt32(8080) || probe.PeriodSeconds != 5 || probe.TimeoutSeconds != 1 || probe.SuccessThreshold != 1 || probe.FailureThreshold != 3 || container.LivenessProbe != nil || container.StartupProbe != nil {
		t.Fatalf("probes = %#v", container)
	}
	if container.Resources.Requests.Cpu().String() != "100m" || container.Resources.Requests.Memory().String() != "64Mi" || container.Resources.Limits.Cpu().String() != "500m" || container.Resources.Limits.Memory().String() != "128Mi" {
		t.Fatalf("resources = %#v", container.Resources)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP || len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 80 || svc.Spec.Ports[0].TargetPort != intstr.FromInt32(8080) || svc.Spec.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("service = %#v", svc.Spec)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" && action.GetVerb() != "create" {
			t.Fatalf("unexpected action %v", action)
		}
	}
}

func TestCreateCollisionDoesNotWrite(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, kind := range []string{"deployment", "service"} {
			t.Run(kind+" managed="+map[bool]string{false: "false", true: "true"}[managed], func(t *testing.T) {
				meta := metav1.ObjectMeta{Name: "collision", Namespace: Namespace, UID: "original"}
				if managed {
					meta.Labels = appLabels(meta.Name)
				}
				var object runtime.Object = &appsv1.Deployment{ObjectMeta: meta}
				if kind == "service" {
					object = &corev1.Service{ObjectMeta: meta}
				}
				client := fake.NewClientset(object)
				_, err := (Apps{Client: client}).Create(context.Background(), AppSpec{Name: meta.Name, Image: "nginx", ContainerPort: 80})
				var appErr *AppError
				if !errors.As(err, &appErr) || appErr.Code != "conflict" || *appErr.Details.Resources != (CreateOutcomes{NotAttempted, NotAttempted}) {
					t.Fatalf("error = %#v", err)
				}
				for _, action := range client.Actions() {
					if action.GetVerb() != "get" {
						t.Fatalf("collision mutated resource: %v", action)
					}
				}
			})
		}
	}
}

func TestCreateWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name, resource, code string
		err                  error
		outcome              CreateOutcome
	}{
		{"deployment race", "deployments", "conflict", apierrors.NewAlreadyExists(deploymentResource, "demo"), Failed},
		{"service race", "services", "conflict", apierrors.NewAlreadyExists(serviceResource, "demo"), Failed},
		{"service forbidden", "services", "upstream_unavailable", apierrors.NewForbidden(serviceResource, "demo", errors.New("denied by policy")), Failed},
		{"service deadline", "services", "upstream_timeout", context.DeadlineExceeded, Unknown},
		{"deployment timeout", "deployments", "upstream_timeout", apierrors.NewTimeoutError("timed out", 0), Unknown},
		{"service transport", "services", "upstream_unavailable", &url.Error{Op: "Post", URL: "https://secret:password@upstream", Err: errors.New("connection lost")}, Unknown},
		{"service unexpected", "services", "internal_error", errors.New("secret upstream details"), Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset()
			client.PrependReactor("create", tc.resource, func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, tc.err })
			_, err := (Apps{Client: client}).Create(context.Background(), AppSpec{Name: "demo", Image: "nginx", ContainerPort: 80})
			var appErr *AppError
			if !errors.As(err, &appErr) || appErr.Code != tc.code {
				t.Fatalf("error = %#v", err)
			}
			want := CreateOutcomes{Created, tc.outcome}
			if tc.resource == "deployments" {
				want = CreateOutcomes{tc.outcome, NotAttempted}
			}
			if *appErr.Details.Resources != want {
				t.Fatalf("outcomes = %#v, want %#v", appErr.Details.Resources, want)
			}
			if strings.Contains(appErr.Message, "secret") || strings.Contains(appErr.Message, "password") {
				t.Fatalf("leaked transport error: %v", appErr)
			}
			dep, getErr := client.AppsV1().Deployments(Namespace).Get(context.Background(), "demo", metav1.GetOptions{})
			if tc.resource == "services" && (getErr != nil || dep.Name != "demo") {
				t.Fatal("created deployment was not retained")
			}
			svc, _ := client.CoreV1().Services(Namespace).List(context.Background(), metav1.ListOptions{})
			if len(svc.Items) != 0 {
				t.Fatal("unexpected service")
			}
		})
	}
}

func TestCreatePreflightFailures(t *testing.T) {
	for _, kind := range []string{"deployments", "services"} {
		t.Run(kind, func(t *testing.T) {
			client := fake.NewClientset()
			client.PrependReactor("get", kind, func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: kind}, "demo", errors.New("access denied"))
			})
			_, err := (Apps{Client: client}).Create(context.Background(), AppSpec{Name: "demo", Image: "nginx", ContainerPort: 80})
			var appErr *AppError
			if !errors.As(err, &appErr) || *appErr.Details.Resources != (CreateOutcomes{NotAttempted, NotAttempted}) || !strings.Contains(appErr.Message, "access denied") {
				t.Fatalf("error = %#v", err)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() != "get" {
					t.Fatalf("preflight failure wrote: %v", action)
				}
			}
		})
	}
}

func TestCreateCancellationLeavesUnissuedWritesNotAttempted(t *testing.T) {
	for _, afterDeployment := range []bool{false, true} {
		t.Run(map[bool]string{false: "before deployment", true: "after deployment"}[afterDeployment], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := fake.NewClientset()
			if afterDeployment {
				client.PrependReactor("create", "deployments", func(clienttesting.Action) (bool, runtime.Object, error) { cancel(); return false, nil, nil })
			} else {
				cancel()
			}
			_, err := (Apps{Client: client}).Create(ctx, AppSpec{Name: "demo", Image: "nginx", ContainerPort: 80})
			var appErr *AppError
			want := CreateOutcomes{NotAttempted, NotAttempted}
			if afterDeployment {
				want.Deployment = Created
			}
			if !errors.As(err, &appErr) || appErr.Code != "upstream_unavailable" || *appErr.Details.Resources != want {
				t.Fatalf("error = %#v, want outcomes %#v", err, want)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "create" && (action.GetResource().Resource == "services" || !afterDeployment) {
					t.Fatalf("unissued write unexpectedly attempted: %v", action)
				}
			}
		})
	}
}
