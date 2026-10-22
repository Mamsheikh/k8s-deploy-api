package kube

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func readySnapshot() InspectResult {
	image := "nginx:v2"
	return InspectResult{
		Resources: AppPresence{Deployment: true, Service: true}, Image: &image,
		Deployment: &DeploymentDiagnostics{Generation: 3, ObservedGeneration: 3, DesiredReplicas: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
		Service:    &ServiceDiagnostics{},
		Pods: []PodDiagnostics{{Name: "current", Phase: corev1.PodRunning, Ready: true, currentTemplate: true, specImage: image,
			Container: &ContainerDiagnostics{Image: image, Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
	}
}

func TestRolloutReady(t *testing.T) {
	now := metav1.Now()
	for _, tc := range []struct {
		name   string
		change func(*InspectResult)
		want   bool
	}{
		{"complete", func(*InspectResult) {}, true},
		{"later observed generation", func(s *InspectResult) { s.Deployment.ObservedGeneration++ }, true},
		{"unobserved generation", func(s *InspectResult) { s.Deployment.ObservedGeneration-- }, false},
		{"unknown generation", func(s *InspectResult) { s.Deployment.Generation = 0 }, false},
		{"missing deployment", func(s *InspectResult) { s.Resources.Deployment = false }, false},
		{"missing service", func(s *InspectResult) { s.Resources.Service = false }, false},
		{"missing deployment diagnostics", func(s *InspectResult) { s.Deployment = nil }, false},
		{"missing service diagnostics", func(s *InspectResult) { s.Service = nil }, false},
		{"deleting deployment", func(s *InspectResult) { s.Deployment.DeletionTimestamp = &now }, false},
		{"deleting service", func(s *InspectResult) { s.Service.DeletionTimestamp = &now }, false},
		{"zero desired", func(s *InspectResult) { s.Deployment.DesiredReplicas = 0 }, false},
		{"two desired", func(s *InspectResult) { s.Deployment.DesiredReplicas = 2 }, false},
		{"no updated replica", func(s *InspectResult) { s.Deployment.UpdatedReplicas = 0 }, false},
		{"surge updated replica", func(s *InspectResult) { s.Deployment.UpdatedReplicas = 2 }, false},
		{"no ready replica", func(s *InspectResult) { s.Deployment.ReadyReplicas = 0 }, false},
		{"two ready replicas", func(s *InspectResult) { s.Deployment.ReadyReplicas = 2 }, false},
		{"no available replica", func(s *InspectResult) { s.Deployment.AvailableReplicas = 0 }, false},
		{"two available replicas", func(s *InspectResult) { s.Deployment.AvailableReplicas = 2 }, false},
		{"old replica remains", func(s *InspectResult) { s.Deployment.Replicas = 2 }, false},
		{"no total replica", func(s *InspectResult) { s.Deployment.Replicas = 0 }, false},
		{"no pods", func(s *InspectResult) { s.Pods = nil }, false},
		{"old healthy pod", func(s *InspectResult) { s.Pods[0].currentTemplate = false }, false},
		{"old healthy and unready current pod", func(s *InspectResult) {
			old := s.Pods[0]
			old.currentTemplate = false
			s.Pods[0].Ready = false
			s.Pods = append(s.Pods, old)
		}, false},
		{"deleting current pod", func(s *InspectResult) { s.Pods[0].DeletionTimestamp = &now }, false},
		{"unready current pod", func(s *InspectResult) { s.Pods[0].Ready = false }, false},
		{"pending current pod", func(s *InspectResult) { s.Pods[0].Phase = corev1.PodPending }, false},
		{"no app container status", func(s *InspectResult) { s.Pods[0].Container = nil }, false},
		{"unready app container", func(s *InspectResult) { s.Pods[0].Container.Ready = false }, false},
		{"app container not running", func(s *InspectResult) {
			s.Pods[0].Container.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}
		}, false},
		{"missing requested image", func(s *InspectResult) { s.Image = nil }, false},
		{"empty requested image", func(s *InspectResult) { *s.Image = "" }, false},
		{"old pod spec with requested status image", func(s *InspectResult) { s.Pods[0].specImage = "nginx:v1" }, false},
		{"missing pod spec image", func(s *InspectResult) { s.Pods[0].specImage = "" }, false},
		{"runtime resolved image", func(s *InspectResult) { s.Pods[0].Container.Image = "docker.io/library/nginx:v2" }, true},
		{"recovery retains raw deadline condition", func(s *InspectResult) {
			s.Deployment.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := readySnapshot()
			tc.change(&snapshot)
			if got := rolloutReady(snapshot); got != tc.want {
				t.Fatalf("ready=%v, want %v", got, tc.want)
			}
		})
	}
}
