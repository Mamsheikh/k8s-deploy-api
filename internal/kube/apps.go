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
	"k8s.io/apimachinery/pkg/runtime/schema"
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

type ResourceOutcome string

const (
	Created         ResourceOutcome = "created"
	Absent          ResourceOutcome = "absent"
	Deleting        ResourceOutcome = "deleting"
	DeleteRequested ResourceOutcome = "delete_requested"
	Failed          ResourceOutcome = "failed"
	Unknown         ResourceOutcome = "unknown"
	NotAttempted    ResourceOutcome = "not_attempted"
)

type ResourceOutcomes struct {
	Deployment ResourceOutcome `json:"deployment"`
	Service    ResourceOutcome `json:"service"`
}

type CreateResult struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
	Service    string `json:"service"`
	StatusURL  string `json:"statusUrl"`
}

type DeleteResult struct {
	Name      string           `json:"name"`
	Namespace string           `json:"namespace"`
	Resources ResourceOutcomes `json:"resources"`
	StatusURL string           `json:"statusUrl"`
}

type ImageUpdateRequest struct {
	Image string `json:"image"`
}

type ImageUpdateResult struct {
	Name      string `json:"name"`
	StatusURL string `json:"statusUrl"`
}

type ResourceFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type ResourceFailures struct {
	Deployment *ResourceFailure `json:"deployment,omitempty"`
	Service    *ResourceFailure `json:"service,omitempty"`
}

type ErrorDetails struct {
	Failures  *ResourceFailures `json:"failures,omitempty"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Resources *ResourceOutcomes `json:"resources,omitempty"`
	Reason    string            `json:"reason"`
}

type AppError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details ErrorDetails `json:"details"`
}

func (e *AppError) Error() string { return e.Message }

type Apps struct{ Client kubernetes.Interface }

func (a Apps) UpdateImage(ctx context.Context, name string, request ImageUpdateRequest) (ImageUpdateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	fail := func(err error) (ImageUpdateResult, error) {
		failure := operationError(err)
		failure.Details.Name = name
		return ImageUpdateResult{}, failure
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	deployments := a.Client.AppsV1().Deployments(Namespace)
	deployment, err := deployments.Get(ctx, name, metav1.GetOptions{})
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
	if deployment != nil && (deployment.Name != name || !managedApp(deployment.ObjectMeta)) {
		return fail(apierrors.NewConflict(deploymentResource, name, errors.New("resource is not managed by deploy-api for this application")))
	}
	if service != nil && (service.Name != name || !managedApp(service.ObjectMeta)) {
		return fail(apierrors.NewConflict(serviceResource, name, errors.New("resource is not managed by deploy-api for this application")))
	}
	if deployment == nil && service == nil {
		return fail(apierrors.NewNotFound(deploymentResource, name))
	}
	if deployment == nil || service == nil {
		return fail(apierrors.NewConflict(deploymentResource, name, errors.New("application is incomplete; delete and recreate it")))
	}
	if deployment.DeletionTimestamp != nil || service.DeletionTimestamp != nil {
		return fail(apierrors.NewConflict(deploymentResource, name, errors.New("application is being deleted")))
	}
	for i, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != "app" {
			continue
		}
		result := ImageUpdateResult{Name: name, StatusURL: "/apps/" + name}
		if container.Image == request.Image {
			return result, nil
		}
		if deployment.UID == "" || deployment.ResourceVersion == "" {
			return fail(errors.New("Kubernetes resource identity is incomplete"))
		}
		deployment.Spec.Template.Spec.Containers[i].Image = request.Image
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if _, err := deployments.Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				err = apierrors.NewConflict(deploymentResource, name, errors.New("application Deployment disappeared during image update; inspect before retrying"))
			}
			return fail(err)
		}
		return result, nil
	}
	return fail(apierrors.NewConflict(deploymentResource, name, errors.New("application Deployment has no app container")))
}

func (a Apps) Create(ctx context.Context, spec AppSpec) (CreateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	outcomes := ResourceOutcomes{Deployment: NotAttempted, Service: NotAttempted}
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

func (a Apps) Delete(ctx context.Context, name string) (DeleteResult, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	deployments := a.Client.AppsV1().Deployments(Namespace)
	services := a.Client.CoreV1().Services(Namespace)
	background := metav1.DeletePropagationBackground
	depOutcome, depErr := deleteResource(ctx, name, deploymentResource,
		func() (metav1.Object, error) { return deployments.Get(ctx, name, metav1.GetOptions{}) },
		func(options metav1.DeleteOptions) error { return deployments.Delete(ctx, name, options) },
		metav1.DeleteOptions{PropagationPolicy: &background})
	svcOutcome, svcErr := deleteResource(ctx, name, serviceResource,
		func() (metav1.Object, error) { return services.Get(ctx, name, metav1.GetOptions{}) },
		func(options metav1.DeleteOptions) error { return services.Delete(ctx, name, options) },
		metav1.DeleteOptions{})
	outcomes := ResourceOutcomes{Deployment: depOutcome, Service: svcOutcome}
	if depErr != nil || svcErr != nil {
		failures := &ResourceFailures{}
		var selected *AppError
		for i, err := range []error{depErr, svcErr} {
			if err == nil {
				continue
			}
			failure := operationError(err)
			detail := &ResourceFailure{Code: failure.Code, Message: failure.Message, Reason: failure.Details.Reason}
			if i == 0 {
				failures.Deployment = detail
			} else {
				failures.Service = detail
			}
			if selected == nil || deleteErrorPriority(failure.Code) > deleteErrorPriority(selected.Code) {
				selected = failure
			}
		}
		selected.Details.Name = name
		selected.Details.Resources = &outcomes
		selected.Details.Failures = failures
		return DeleteResult{}, selected
	}
	return DeleteResult{Name: name, Namespace: Namespace, Resources: outcomes, StatusURL: "/apps/" + name}, nil
}

func deleteResource(ctx context.Context, name string, resource schema.GroupResource, get func() (metav1.Object, error), remove func(metav1.DeleteOptions) error, options metav1.DeleteOptions) (ResourceOutcome, error) {
	if err := ctx.Err(); err != nil {
		return NotAttempted, err
	}
	object, err := get()
	if apierrors.IsNotFound(err) {
		return Absent, nil
	}
	if err != nil {
		return NotAttempted, err
	}
	meta := metav1.ObjectMeta{Name: object.GetName(), Namespace: object.GetNamespace(), Labels: object.GetLabels()}
	if object.GetName() != name || !managedApp(meta) {
		return NotAttempted, apierrors.NewConflict(resource, name, errors.New("resource is not managed by deploy-api for this application"))
	}
	uid, version := object.GetUID(), object.GetResourceVersion()
	if uid == "" || version == "" {
		return NotAttempted, errors.New("Kubernetes resource identity is incomplete")
	}
	if object.GetDeletionTimestamp() != nil {
		return Deleting, nil
	}
	if err := ctx.Err(); err != nil {
		return NotAttempted, err
	}
	options.Preconditions = &metav1.Preconditions{UID: &uid, ResourceVersion: &version}
	err = remove(options)
	if apierrors.IsNotFound(err) {
		return Absent, nil
	}
	if err != nil {
		return writeOutcome(err), err
	}
	return DeleteRequested, nil
}

func deleteErrorPriority(code string) int {
	switch code {
	case "upstream_timeout":
		return 4
	case "upstream_unavailable":
		return 3
	case "internal_error":
		return 2
	default:
		return 1
	}
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

func writeOutcome(err error) ResourceOutcome {
	var status apierrors.APIStatus
	if errors.As(err, &status) && !apierrors.IsTimeout(err) && !apierrors.IsServerTimeout(err) && !apierrors.IsInternalError(err) {
		return Failed
	}
	return Unknown
}

func createError(name string, outcomes ResourceOutcomes, err error) *AppError {
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
