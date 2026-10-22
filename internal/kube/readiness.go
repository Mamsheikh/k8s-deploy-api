package kube

import corev1 "k8s.io/api/core/v1"

func rolloutReady(snapshot InspectResult) bool {
	d := snapshot.Deployment
	if !snapshot.Resources.Deployment || !snapshot.Resources.Service || d == nil || snapshot.Service == nil ||
		d.DeletionTimestamp != nil || snapshot.Service.DeletionTimestamp != nil ||
		d.Generation < 1 || d.ObservedGeneration < d.Generation ||
		d.DesiredReplicas != 1 || d.UpdatedReplicas != 1 || d.ReadyReplicas != 1 || d.AvailableReplicas != 1 || d.Replicas != 1 ||
		snapshot.Image == nil || *snapshot.Image == "" {
		return false
	}
	for _, pod := range snapshot.Pods {
		if pod.currentTemplate && pod.specImage == *snapshot.Image && pod.DeletionTimestamp == nil &&
			pod.Phase == corev1.PodRunning && pod.Ready && pod.Container != nil && pod.Container.Ready && pod.Container.State.Running != nil {
			return true
		}
	}
	return false
}
