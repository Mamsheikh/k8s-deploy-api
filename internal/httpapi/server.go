package httpapi

import (
	"encoding/json"
	"net/http"
)

func NewHandler(apps AppService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /apps", createApp(apps))
	mux.HandleFunc("GET /apps", listApps(apps))
	mux.HandleFunc("GET /apps/{name}", inspectApp(apps))
	mux.HandleFunc("GET /apps/{name}/logs", appLogs(apps))
	mux.HandleFunc("DELETE /apps/{name}", deleteApp(apps))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Status string `json:"status"`
		}{Status: "ok"})
	})
	return mux
}
