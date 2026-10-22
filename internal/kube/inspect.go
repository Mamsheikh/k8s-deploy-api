package kube

import (
	"context"
	"errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InspectResult struct {
	Name          string                 `json:"name"`
	Namespace     string                 `json:"namespace"`
	Image         *string                `json:"image,omitempty"`
	ContainerPort *int32                 `json:"containerPort,omitempty"`
	Resources     AppPresence            `json:"resources"`
	Ready         bool                   `json:"ready"`
	Deployment    *DeploymentDiagnostics `json:"deployment,omitempty"`
	Service       *ServiceDiagnostics    `json:"service,omitempty"`
	Pods          []PodDiagnostics       `json:"pods"`
}

type DeploymentDiagnostics struct {
	DeletionTimestamp  *metav1.Time                 `json:"deletionTimestamp,omitempty"`
	Generation         int64                        `json:"generation"`
	ObservedGeneration int64                        `json:"observedGeneration"`
	DesiredReplicas    int32                        `json:"desiredReplicas"`
	Replicas           int32                        `json:"replicas"`
	UpdatedReplicas    int32                        `json:"updatedReplicas"`
	ReadyReplicas      int32                        `json:"readyReplicas"`
	AvailableReplicas  int32                        `json:"availableReplicas"`
	Conditions         []appsv1.DeploymentCondition `json:"conditions"`
}

type ServiceDiagnostics struct {
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty"`
}

type PodDiagnostics struct {
	Name              string                `json:"name"`
	DeletionTimestamp *metav1.Time          `json:"deletionTimestamp,omitempty"`
	Phase             corev1.PodPhase       `json:"phase"`
	Ready             bool                  `json:"ready"`
	Container         *ContainerDiagnostics `json:"container,omitempty"`
	currentTemplate   bool
	specImage         string
}

type ContainerDiagnostics struct {
	Image                string                `json:"image"`
	ImageID              string                `json:"imageID,omitempty"`
	Ready                bool                  `json:"ready"`
	RestartCount         int32                 `json:"restartCount"`
	State                corev1.ContainerState `json:"state"`
	LastTerminationState corev1.ContainerState `json:"lastTerminationState"`
}

func (a Apps) Inspect(ctx context.Context, name string) (InspectResult, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	fail := func(err error) (InspectResult, error) {
		failure := operationError(err)
		failure.Details.Name = name
		return InspectResult{}, failure
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	deployment, err := a.Client.AppsV1().Deployments(Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		deployment = nil
	} else if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	service, err := a.Client.CoreV1().Services(Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		service = nil
	} else if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if deployment != nil && !managedApp(deployment.ObjectMeta) {
		return fail(apierrors.NewConflict(deploymentResource, name, errors.New("resource is not managed by deploy-api for this application")))
	}
	if service != nil && !managedApp(service.ObjectMeta) {
		return fail(apierrors.NewConflict(serviceResource, name, errors.New("resource is not managed by deploy-api for this application")))
	}
	if deployment == nil && service == nil {
		return fail(apierrors.NewNotFound(deploymentResource, name))
	}
	result := InspectResult{Name: name, Namespace: Namespace, Resources: AppPresence{Deployment: deployment != nil, Service: service != nil}, Pods: []PodDiagnostics{}}
	if service != nil {
		result.Service = &ServiceDiagnostics{DeletionTimestamp: service.DeletionTimestamp}
	}
	if deployment == nil {
		return result, nil
	}
	status := deployment.Status
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	conditions := append([]appsv1.DeploymentCondition{}, status.Conditions...)
	result.Deployment = &DeploymentDiagnostics{
		DeletionTimestamp: deployment.DeletionTimestamp, Generation: deployment.Generation, ObservedGeneration: status.ObservedGeneration,
		DesiredReplicas: desired, Replicas: status.Replicas, UpdatedReplicas: status.UpdatedReplicas, ReadyReplicas: status.ReadyReplicas, AvailableReplicas: status.AvailableReplicas, Conditions: conditions,
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "app" {
			result.Image = &container.Image
			if len(container.Ports) > 0 {
				result.ContainerPort = &container.Ports[0].ContainerPort
			}
			break
		}
	}
	ownership, err := a.resolveOwnership(ctx, deployment)
	var pods []corev1.Pod
	if err == nil {
		pods, err = a.listOwnedPods(ctx, ownership)
	}
	if err != nil {
		failure := operationError(err)
		if failure.Code == "not_found" {
			failure.Code = "upstream_unavailable"
		}
		failure.Details.Name = name
		return InspectResult{}, failure
	}
	for _, pod := range pods {
		diagnostics := projectPod(pod)
		owner := metav1.GetControllerOf(&pod)
		diagnostics.currentTemplate = ownership.replicaSets[owner.UID].currentTemplate
		result.Pods = append(result.Pods, diagnostics)
	}
	result.Ready = rolloutReady(result)
	return result, nil
}

func projectPod(pod corev1.Pod) PodDiagnostics {
	result := PodDiagnostics{Name: pod.Name, DeletionTimestamp: pod.DeletionTimestamp, Phase: pod.Status.Phase}
	for _, container := range pod.Spec.Containers {
		if container.Name == "app" {
			result.specImage = container.Image
			break
		}
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			result.Ready = condition.Status == corev1.ConditionTrue
			break
		}
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == "app" {
			result.Container = &ContainerDiagnostics{Image: container.Image, ImageID: container.ImageID, Ready: container.Ready, RestartCount: container.RestartCount, State: container.State, LastTerminationState: container.LastTerminationState}
			break
		}
	}
	return result
}
