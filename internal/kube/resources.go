package kube

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	deploymentResource = schema.GroupResource{Group: "apps", Resource: "deployments"}
	serviceResource    = schema.GroupResource{Resource: "services"}
)

func appLabels(name string) map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": name}
}

func deployment(spec AppSpec) *appsv1.Deployment {
	replicas, progress := int32(1), int32(120)
	surge, unavailable := intstr.FromInt32(1), intstr.FromInt32(0)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: Namespace, Labels: appLabels(spec.Name)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas, ProgressDeadlineSeconds: &progress,
			Selector: &metav1.LabelSelector{MatchLabels: appLabels(spec.Name)},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: &surge, MaxUnavailable: &unavailable}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: appLabels(spec.Name)},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "app", Image: spec.Image, ImagePullPolicy: corev1.PullAlways,
					Ports:          []corev1.ContainerPort{{ContainerPort: spec.ContainerPort, Protocol: corev1.ProtocolTCP}},
					ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(spec.ContainerPort)}}, PeriodSeconds: 5, TimeoutSeconds: 1, SuccessThreshold: 1, FailureThreshold: 3},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
						Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
					},
				}}},
			},
		},
	}
}

func service(spec AppSpec) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: Namespace, Labels: appLabels(spec.Name)},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: appLabels(spec.Name), Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(spec.ContainerPort), Protocol: corev1.ProtocolTCP}}},
	}
}
