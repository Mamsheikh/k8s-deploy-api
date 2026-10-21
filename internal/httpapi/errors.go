package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"k8s-deploy-api/internal/kube"
)

type errorEnvelope struct {
	Error *kube.AppError `json:"error"`
}

func writeError(w http.ResponseWriter, err error) {
	var appErr *kube.AppError
	if !errors.As(err, &appErr) {
		appErr = &kube.AppError{Code: "internal_error", Message: "Unexpected internal error"}
	}
	status := http.StatusInternalServerError
	switch appErr.Code {
	case "invalid_request":
		status = http.StatusBadRequest
	case "not_found":
		status = http.StatusNotFound
	case "conflict":
		status = http.StatusConflict
	case "upstream_unavailable":
		status = http.StatusServiceUnavailable
	case "upstream_timeout":
		status = http.StatusGatewayTimeout
	}
	writeJSON(w, status, errorEnvelope{Error: appErr})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
