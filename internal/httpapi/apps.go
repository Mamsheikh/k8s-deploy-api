package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"k8s-deploy-api/internal/kube"
)

type AppCreator interface {
	Create(context.Context, kube.AppSpec) (kube.CreateResult, error)
}

type AppLister interface {
	List(context.Context) (kube.ListResult, error)
}

type AppDeleter interface {
	Delete(context.Context, string) (kube.DeleteResult, error)
}

type AppInspector interface {
	Inspect(context.Context, string) (kube.InspectResult, error)
}

type AppImageUpdater interface {
	UpdateImage(context.Context, string, kube.ImageUpdateRequest) (kube.ImageUpdateResult, error)
}

type AppService interface {
	AppImageUpdater
	AppLogger
	AppInspector
	AppDeleter
	AppCreator
	AppLister
}

func listApps(lister AppLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := lister.List(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

var appName = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

func updateAppImage(updater AppImageUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !appName.MatchString(name) {
			writeError(w, invalidRequest("Name must be 1 to 63 lowercase letters, digits or hyphens, starting with a letter and ending with a letter or digit"))
			return
		}
		var request kube.ImageUpdateRequest
		if err := decodeObject(r.Body, &request); err != nil {
			writeError(w, invalidRequest("Body must be one JSON object with only image"))
			return
		}
		if strings.TrimSpace(request.Image) == "" {
			writeError(w, invalidRequest("Image must be nonempty"))
			return
		}
		result, err := updater.UpdateImage(r.Context(), name, request)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Location", result.StatusURL)
		writeJSON(w, http.StatusAccepted, result)
	}
}

func createApp(creator AppCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var spec kube.AppSpec
		if err := decodeObject(r.Body, &spec); err != nil {
			writeError(w, invalidRequest("Body must be one JSON object with only name, image, and integer containerPort"))
			return
		}
		if !appName.MatchString(spec.Name) || strings.TrimSpace(spec.Image) == "" || spec.ContainerPort < 1 || spec.ContainerPort > 65535 {
			writeError(w, invalidRequest("Name must be 1 to 63 lowercase letters, digits or hyphens, starting with a letter and ending with a letter or digit; image must be nonempty; containerPort must be an integer from 1 to 65535"))
			return
		}
		result, err := creator.Create(r.Context(), spec)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Location", result.StatusURL)
		writeJSON(w, http.StatusAccepted, result)
	}
}

func invalidRequest(message string) *kube.AppError {
	return &kube.AppError{Code: "invalid_request", Message: message, Details: kube.ErrorDetails{Namespace: kube.Namespace, Resources: &kube.ResourceOutcomes{Deployment: kube.NotAttempted, Service: kube.NotAttempted}, Reason: "InvalidRequest"}}
}

func deleteApp(deleter AppDeleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !appName.MatchString(name) {
			writeError(w, invalidRequest("Name must be 1 to 63 lowercase letters, digits or hyphens, starting with a letter and ending with a letter or digit"))
			return
		}
		result, err := deleter.Delete(r.Context(), name)
		if err != nil {
			writeError(w, err)
			return
		}
		if result.Resources.Deployment == kube.Absent && result.Resources.Service == kube.Absent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Location", result.StatusURL)
		writeJSON(w, http.StatusAccepted, result)
	}
}

func inspectApp(inspector AppInspector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !appName.MatchString(name) {
			writeError(w, invalidRequest("Name must be 1 to 63 lowercase letters, digits or hyphens, starting with a letter and ending with a letter or digit"))
			return
		}
		result, err := inspector.Inspect(r.Context(), name)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
