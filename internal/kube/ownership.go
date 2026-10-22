package kube

import (
	"context"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

type appOwnership struct {
	name        string
	replicaSets map[types.UID]ownedReplicaSet
}

type ownedReplicaSet struct {
	name            string
	currentTemplate bool
}

func (a Apps) resolveOwnership(ctx context.Context, deployment *appsv1.Deployment) (appOwnership, error) {
	options := metav1.ListOptions{LabelSelector: labels.Set(appLabels(deployment.Name)).String()}
	sets, err := a.Client.AppsV1().ReplicaSets(Namespace).List(ctx, options)
	if err != nil {
		return appOwnership{}, err
	}
	if err := ctx.Err(); err != nil {
		return appOwnership{}, err
	}
	ownership := appOwnership{name: deployment.Name, replicaSets: make(map[types.UID]ownedReplicaSet)}
	for _, set := range sets.Items {
		if appMember(set.ObjectMeta, deployment.Name) && controlledBy(set.ObjectMeta, "Deployment", deployment.Name, deployment.UID) && set.UID != "" {
			ownership.replicaSets[set.UID] = ownedReplicaSet{
				name: set.Name, currentTemplate: set.DeletionTimestamp == nil && equalPodTemplates(set.Spec.Template, deployment.Spec.Template),
			}
		}
	}
	return ownership, nil
}

func (o appOwnership) ownsPod(pod *corev1.Pod) bool {
	owner := metav1.GetControllerOf(pod)
	if !appMember(pod.ObjectMeta, o.name) || owner == nil {
		return false
	}
	set, ok := o.replicaSets[owner.UID]
	return ok && controlledBy(pod.ObjectMeta, "ReplicaSet", set.name, owner.UID)
}

func (a Apps) ownedPods(ctx context.Context, deployment *appsv1.Deployment) ([]corev1.Pod, error) {
	ownership, err := a.resolveOwnership(ctx, deployment)
	if err != nil {
		return nil, err
	}
	return a.listOwnedPods(ctx, ownership)
}

func (a Apps) listOwnedPods(ctx context.Context, ownership appOwnership) ([]corev1.Pod, error) {
	options := metav1.ListOptions{LabelSelector: labels.Set(appLabels(ownership.name)).String()}
	pods, err := a.Client.CoreV1().Pods(Namespace).List(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned := make([]corev1.Pod, 0)
	for _, pod := range pods.Items {
		if ownership.ownsPod(&pod) {
			owned = append(owned, pod)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Name < owned[j].Name })
	return owned, nil
}

func equalPodTemplates(left, right corev1.PodTemplateSpec) bool {
	a, b := left.DeepCopy(), right.DeepCopy()
	delete(a.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	delete(b.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	return equality.Semantic.DeepEqual(a, b)
}

func appMember(meta metav1.ObjectMeta, name string) bool {
	return meta.Namespace == Namespace && meta.Labels["app.kubernetes.io/managed-by"] == "deploy-api" && meta.Labels["app.kubernetes.io/name"] == name
}

func controlledBy(meta metav1.ObjectMeta, kind, name string, uid types.UID) bool {
	owner := metav1.GetControllerOf(&meta)
	return uid != "" && owner != nil && owner.APIVersion == "apps/v1" && owner.Kind == kind && owner.Name == name && owner.UID == uid
}
