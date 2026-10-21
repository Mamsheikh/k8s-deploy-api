package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			err := run(context.Background(), path, func(context.Context) error { started = true; return nil })
			wantStarted := tc.code == http.StatusOK
			if started != wantStarted || (err == nil) != wantStarted {
				t.Fatalf("error = %v, server started = %v, want started = %v", err, started, wantStarted)
			}
		})
	}
}
