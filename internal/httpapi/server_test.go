package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	w := httptest.NewRecorder()
	NewHandler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 1 || response["status"] != "ok" {
		t.Errorf("response = %#v, want only status ok", response)
	}
}

func TestRouting(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPut, "/apps", http.StatusMethodNotAllowed},
		{http.MethodGet, "/healthz/extra", http.StatusNotFound},
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/unknown", http.StatusNotFound},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodPut, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodHead, "/healthz", http.StatusOK},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			NewHandler(nil).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d", w.Code, tc.status)
			}
			if tc.status == http.StatusMethodNotAllowed {
				allow := "GET, HEAD"
				if tc.path == "/apps" {
					allow = "GET, HEAD, POST"
				}
				if w.Header().Get("Allow") != allow {
					t.Errorf("Allow = %q, want %q", w.Header().Get("Allow"), allow)
				}
			}
		})
	}
}
