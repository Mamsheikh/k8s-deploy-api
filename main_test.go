package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunStartsOnlyAfterSuccessfulStartup(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		{"startup succeeds", http.StatusOK},
		{"missing kubeconfig", 0},
		{"namespace absent", http.StatusNotFound},
		{"namespace forbidden", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if tc.code != 0 {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/version" {
						_, _ = fmt.Fprint(w, `{"gitVersion":"v1.34.1"}`)
						return
					}
					if tc.code == http.StatusOK {
						_, _ = fmt.Fprint(w, `{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"deploy-api"}}`)
						return
					}
					w.WriteHeader(tc.code)
					_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","code":%d}`, tc.code)
				}))
				defer upstream.Close()
				contents := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: docker-desktop\n  context:\n    cluster: test\n", upstream.URL)
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			started := false
			err := run(context.Background(), path, func(context.Context, http.Handler) error { started = true; return nil })
			wantStarted := tc.code == http.StatusOK
			if started != wantStarted || (err == nil) != wantStarted {
				t.Fatalf("error = %v, server started = %v, want started = %v", err, started, wantStarted)
			}
		})
	}
}

func TestRunWiresCheckedClientIntoAppHandler(t *testing.T) {
	var calls []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/version":
			_, _ = fmt.Fprint(w, `{"gitVersion":"v1.34.1"}`)
		case r.URL.Path == "/api/v1/namespaces/deploy-api":
			_, _ = fmt.Fprint(w, `{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"deploy-api"}}`)
		case r.Method == http.MethodGet:
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		default:
			w.WriteHeader(201)
			kind, version := "Deployment", "apps/v1"
			if strings.HasSuffix(r.URL.Path, "/services") {
				kind, version = "Service", "v1"
			}
			_, _ = fmt.Fprintf(w, `{"kind":%q,"apiVersion":%q,"metadata":{"name":"demo"}}`, kind, version)
		}
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "config")
	config := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: docker-desktop\n  context:\n    cluster: test\n", upstream.URL)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), path, func(_ context.Context, handler http.Handler) error {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(`{"name":"demo","image":"nginx","containerPort":80}`)))
		if response.Code != 202 {
			t.Fatalf("POST = %d %s", response.Code, response.Body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /version", "GET /api/v1/namespaces/deploy-api", "GET /apis/apps/v1/namespaces/deploy-api/deployments/demo", "GET /api/v1/namespaces/deploy-api/services/demo", "POST /apis/apps/v1/namespaces/deploy-api/deployments", "POST /api/v1/namespaces/deploy-api/services"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
