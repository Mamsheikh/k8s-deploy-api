package kube

import (
	"context"
	"errors"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (a Apps) Logs(ctx context.Context, name, podName string, tailLines int64) (io.ReadCloser, error) {
	return a.logs(ctx, name, podName, tailLines, OperationTimeout)
}

func (a Apps) logs(ctx context.Context, name, podName string, tailLines int64, timeout time.Duration) (io.ReadCloser, error) {
	setup, done := context.WithTimeout(ctx, timeout)
	defer done()
	fail := func(err error) (io.ReadCloser, error) {
		failure := operationError(err)
		failure.Details.Name = name
		return nil, failure
	}
	deployment, err := a.Client.AppsV1().Deployments(Namespace).Get(setup, name, metav1.GetOptions{})
	if err != nil {
		return fail(err)
	}
	if !managedApp(deployment.ObjectMeta) {
		return fail(apierrors.NewConflict(deploymentResource, name, errors.New("resource is not managed by deploy-api for this application")))
	}
	ownership, err := a.resolveOwnership(setup, deployment)
	if err != nil {
		return fail(err)
	}
	pod, err := a.Client.CoreV1().Pods(Namespace).Get(setup, podName, metav1.GetOptions{})
	if err != nil {
		return fail(err)
	}
	if !ownership.ownsPod(pod) {
		return fail(apierrors.NewConflict(corev1.Resource("pods"), podName, errors.New("Pod does not belong to this application's Deployment")))
	}
	deadline, _ := setup.Deadline()
	streamCtx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(time.Until(deadline), func() { cancel(context.DeadlineExceeded) })
	body, err := a.Client.CoreV1().Pods(Namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: "app", Follow: true, TailLines: &tailLines,
	}).Stream(streamCtx)
	if !timer.Stop() {
		if body != nil {
			_ = body.Close()
		}
		cancel(context.DeadlineExceeded)
		return fail(context.DeadlineExceeded)
	}
	if err != nil {
		cancel(err)
		if apierrors.IsBadRequest(err) {
			failure := operationError(err)
			failure.Code = "conflict"
			failure.Details.Name = name
			return nil, failure
		}
		return fail(err)
	}
	return &logStream{ReadCloser: body, cancel: cancel}, nil
}

type logStream struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (s *logStream) Close() error {
	s.cancel(context.Canceled)
	return s.ReadCloser.Close()
}
