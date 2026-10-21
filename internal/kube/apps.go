package kube

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const OperationTimeout = 10 * time.Second

type AppSpec struct {
	Name          string `json:"name"`
	Image         string `json:"image"`
	ContainerPort int32  `json:"containerPort"`
}

type CreateOutcome string

const (
	Created      CreateOutcome = "created"
	Failed       CreateOutcome = "failed"
	Unknown      CreateOutcome = "unknown"
	NotAttempted CreateOutcome = "not_attempted"
)

type CreateOutcomes struct {
	Deployment CreateOutcome `json:"deployment"`
	Service    CreateOutcome `json:"service"`
}

type CreateResult struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
	Service    string `json:"service"`
	StatusURL  string `json:"statusUrl"`
}

type ErrorDetails struct {
	Name      string         `json:"name"`
	Namespace string         `json:"namespace"`
	Resources CreateOutcomes `json:"resources"`
	Reason    string         `json:"reason"`
}

type AppError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details ErrorDetails `json:"details"`
}

func (e *AppError) Error() string { return e.Message }

type Apps struct{ Client kubernetes.Interface }

func (a Apps) Create(ctx context.Context, spec AppSpec) (CreateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	outcomes := CreateOutcomes{Deployment: NotAttempted, Service: NotAttempted}
	fail := func(err error) (CreateResult, error) {
		return CreateResult{}, createError(spec.Name, outcomes, err)
	}
	deployments := a.Client.AppsV1().Deployments(Namespace)
	services := a.Client.CoreV1().Services(Namespace)
	if _, err := deployments.Get(ctx, spec.Name, metav1.GetOptions{}); err == nil {
		return fail(apierrors.NewAlreadyExists(deploymentResource, spec.Name))
	} else if !apierrors.IsNotFound(err) {
		return fail(err)
	}
	if _, err := services.Get(ctx, spec.Name, metav1.GetOptions{}); err == nil {
		return fail(apierrors.NewAlreadyExists(serviceResource, spec.Name))
	} else if !apierrors.IsNotFound(err) {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, err := deployments.Create(ctx, deployment(spec), metav1.CreateOptions{}); err != nil {
		outcomes.Deployment = writeOutcome(err)
		return fail(err)
	}
	outcomes.Deployment = Created
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, err := services.Create(ctx, service(spec), metav1.CreateOptions{}); err != nil {
		outcomes.Service = writeOutcome(err)
		return fail(err)
	}
	return CreateResult{Name: spec.Name, Namespace: Namespace, Deployment: spec.Name, Service: spec.Name, StatusURL: "/apps/" + spec.Name}, nil
}

func writeOutcome(err error) CreateOutcome {
	var status apierrors.APIStatus
	if errors.As(err, &status) && !apierrors.IsTimeout(err) && !apierrors.IsServerTimeout(err) && !apierrors.IsInternalError(err) {
		return Failed
	}
	return Unknown
}

func createError(name string, outcomes CreateOutcomes, err error) *AppError {
	code, message, reason := "internal_error", "Unexpected error communicating with Kubernetes", "InternalError"
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		code, message, reason = "upstream_timeout", "Kubernetes operation timed out; inspect before retrying", "Timeout"
	case apierrors.IsAlreadyExists(err), apierrors.IsConflict(err):
		code, message, reason = "conflict", "Application resource already exists or conflicts; inspect before retrying", "Conflict"
	case apierrors.IsNotFound(err):
		code, message, reason = "not_found", "Kubernetes resource was not found", "NotFound"
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err), apierrors.IsServiceUnavailable(err), apierrors.IsTooManyRequests(err), errors.As(err, &network), errors.Is(err, context.Canceled), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		code, message, reason = "upstream_unavailable", "Kubernetes is unavailable or access was denied", "Unavailable"
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		detail := status.Status()
		if detail.Reason != "" {
			reason = string(detail.Reason)
		}
		if detail.Message != "" {
			message = detail.Message
		}
	}
	return &AppError{Code: code, Message: message, Details: ErrorDetails{Name: name, Namespace: Namespace, Resources: outcomes, Reason: reason}}
}
