package kube

import (
	"context"
	"errors"
	"io"
	"net"
	"sort"
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

type AppPresence struct {
	Deployment bool `json:"deployment"`
	Service    bool `json:"service"`
}

type AppSummary struct {
	Name      string      `json:"name"`
	Namespace string      `json:"namespace"`
	Resources AppPresence `json:"resources"`
}

type ListResult struct {
	Apps []AppSummary `json:"apps"`
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
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Resources *CreateOutcomes `json:"resources,omitempty"`
	Reason    string          `json:"reason"`
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

func (a Apps) List(ctx context.Context) (ListResult, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	fail := func(err error) (ListResult, error) {
		failure := operationError(err)
		if failure.Code == "not_found" {
			failure.Code = "upstream_unavailable"
		}
		return ListResult{}, failure
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	options := metav1.ListOptions{LabelSelector: "app.kubernetes.io/managed-by=deploy-api,app.kubernetes.io/name"}
	deployments, err := a.Client.AppsV1().Deployments(Namespace).List(ctx, options)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	services, err := a.Client.CoreV1().Services(Namespace).List(ctx, options)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	presence := make(map[string]AppPresence)
	for _, item := range deployments.Items {
		if managedApp(item.ObjectMeta) {
			resources := presence[item.Name]
			resources.Deployment = true
			presence[item.Name] = resources
		}
	}
	for _, item := range services.Items {
		if managedApp(item.ObjectMeta) {
			resources := presence[item.Name]
			resources.Service = true
			presence[item.Name] = resources
		}
	}
	result := ListResult{Apps: make([]AppSummary, 0, len(presence))}
	for name, resources := range presence {
		result.Apps = append(result.Apps, AppSummary{Name: name, Namespace: Namespace, Resources: resources})
	}
	sort.Slice(result.Apps, func(i, j int) bool { return result.Apps[i].Name < result.Apps[j].Name })
	return result, nil
}

func managedApp(meta metav1.ObjectMeta) bool {
	return meta.Namespace == Namespace && meta.Labels["app.kubernetes.io/managed-by"] == "deploy-api" && meta.Labels["app.kubernetes.io/name"] == meta.Name
}

func writeOutcome(err error) CreateOutcome {
	var status apierrors.APIStatus
	if errors.As(err, &status) && !apierrors.IsTimeout(err) && !apierrors.IsServerTimeout(err) && !apierrors.IsInternalError(err) {
		return Failed
	}
	return Unknown
}

func createError(name string, outcomes CreateOutcomes, err error) *AppError {
	failure := operationError(err)
	failure.Details.Name = name
	failure.Details.Resources = &outcomes
	var status apierrors.APIStatus
	if !errors.As(err, &status) || status.Status().Message == "" {
		switch failure.Code {
		case "upstream_timeout", "conflict":
			failure.Message += "; inspect before retrying"
		}
	}
	return failure
}

func operationError(err error) *AppError {
	code, message, reason := "internal_error", "Unexpected error communicating with Kubernetes", "InternalError"
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		code, message, reason = "upstream_timeout", "Kubernetes operation timed out", "Timeout"
	case apierrors.IsAlreadyExists(err), apierrors.IsConflict(err):
		code, message, reason = "conflict", "Application resource already exists or conflicts", "Conflict"
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
	return &AppError{Code: code, Message: message, Details: ErrorDetails{Namespace: Namespace, Reason: reason}}
}
