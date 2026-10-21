package kube

import (
	"context"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

type appOwnership struct {
	name        string
	replicaSets map[types.UID]string
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
	ownership := appOwnership{name: deployment.Name, replicaSets: make(map[types.UID]string)}
	for _, set := range sets.Items {
		if appMember(set.ObjectMeta, deployment.Name) && controlledBy(set.ObjectMeta, "Deployment", deployment.Name, deployment.UID) && set.UID != "" {
			ownership.replicaSets[set.UID] = set.Name
		}
	}
	return ownership, nil
}

func (o appOwnership) ownsPod(pod *corev1.Pod) bool {
	owner := metav1.GetControllerOf(pod)
	if !appMember(pod.ObjectMeta, o.name) || owner == nil {
		return false
	}
	name, ok := o.replicaSets[owner.UID]
	return ok && controlledBy(pod.ObjectMeta, "ReplicaSet", name, owner.UID)
}

func (a Apps) ownedPods(ctx context.Context, deployment *appsv1.Deployment) ([]corev1.Pod, error) {
	ownership, err := a.resolveOwnership(ctx, deployment)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: labels.Set(appLabels(deployment.Name)).String()}
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

func appMember(meta metav1.ObjectMeta, name string) bool {
	return meta.Namespace == Namespace && meta.Labels["app.kubernetes.io/managed-by"] == "deploy-api" && meta.Labels["app.kubernetes.io/name"] == name
}

func controlledBy(meta metav1.ObjectMeta, kind, name string, uid types.UID) bool {
	owner := metav1.GetControllerOf(&meta)
	return uid != "" && owner != nil && owner.APIVersion == "apps/v1" && owner.Kind == kind && owner.Name == name && owner.UID == uid
}
