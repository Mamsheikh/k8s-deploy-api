package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s-deploy-api/internal/kube"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDashboardAssets(t *testing.T) {
	for _, tc := range []struct {
		path        string
		contentType string
	}{
		{"/", "text/html; charset=utf-8"},
		{"/assets/app.css", "text/css; charset=utf-8"},
		{"/assets/app.js", "text/javascript; charset=utf-8"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				w := httptest.NewRecorder()
				NewHandler(nil).ServeHTTP(w, httptest.NewRequest(method, tc.path, nil))
				if w.Code != http.StatusOK || w.Header().Get("Content-Type") != tc.contentType {
					t.Fatalf("%s response = %d %q", method, w.Code, w.Header().Get("Content-Type"))
				}
				if method == http.MethodGet && w.Body.Len() == 0 {
					t.Fatal("GET returned an empty embedded asset")
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Fatal("HEAD returned a response body")
				}
				if w.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatal("asset is missing MIME sniffing protection")
				}
			}
		})
	}
}

func TestDashboardDoesNotFallbackForUnknownPaths(t *testing.T) {
	for _, path := range []string{"/unknown", "/index.html", "/assets/", "/assets/missing.js", "/assets/app.js/extra", "/apps/example/unknown", "/healthz/extra", "/web/app.js"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			NewHandler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("unknown route received the dashboard")
			}
		})
	}
}

func TestDashboardPreservesAPINotFound(t *testing.T) {
	w := httptest.NewRecorder()
	NewHandler(kube.Apps{Client: fake.NewClientset()}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/apps/missing", nil))
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if !strings.Contains(w.Body.String(), `"code":"not_found"`) {
		t.Fatalf("missing structured API error = %s", w.Body)
	}
}
