package kube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func configFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func configYAML(server string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: target
  cluster:
    server: %s
- name: other
  cluster:
    server: https://unselected.invalid
contexts:
- name: docker-desktop
  context:
    cluster: target
- name: other
  context:
    cluster: other
current-context: other
`, server)
}

func TestLoad(t *testing.T) {
	defaultPath := configFile(t, configYAML("https://default.invalid"))
	t.Setenv("KUBECONFIG", defaultPath)
	for _, tc := range []struct {
		name, path, server, errorText string
	}{
		{name: "default loading rules and fixed context", server: "https://default.invalid"},
		{name: "explicit overrides default", path: configFile(t, configYAML("https://explicit.invalid")), server: "https://explicit.invalid"},
		{name: "missing explicit cannot fall back", path: filepath.Join(t.TempDir(), "missing"), errorText: "no such file"},
		{name: "invalid explicit cannot fall back", path: configFile(t, "clusters: ["), errorText: "load kubeconfig"},
		{name: "missing fixed context", path: configFile(t, strings.ReplaceAll(configYAML("https://unused.invalid"), "docker-desktop", "absent")), errorText: "docker-desktop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := Load(tc.path)
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("error = %v, want %q", err, tc.errorText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := client.Discovery().RESTClient().Get().AbsPath("/version").URL().String(); got != tc.server+"/version" {
				t.Errorf("version URL = %q, want %q", got, tc.server+"/version")
			}
		})
	}
}

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name                string
		versionCode, nsCode int
		versionBody         string
		wantError           string
	}{
		{"success", 200, 200, `{"gitVersion":"v1.34.1"}`, ""},
		{"version unavailable", 503, 200, `{"message":"unavailable"}`, "get Kubernetes server version"},
		{"version malformed", 200, 200, `{`, "decode Kubernetes server version"},
		{"namespace absent", 200, 404, `{"gitVersion":"v1.34.1"}`, "NotFound"},
		{"namespace forbidden", 200, 403, `{"gitVersion":"v1.34.1"}`, "Forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/version":
					w.WriteHeader(tc.versionCode)
					_, _ = fmt.Fprint(w, tc.versionBody)
				case "/api/v1/namespaces/deploy-api":
					w.WriteHeader(tc.nsCode)
					switch tc.nsCode {
					case 200:
						_, _ = fmt.Fprint(w, `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"deploy-api"}}`)
					case 404:
						_, _ = fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","message":"namespace deploy-api absent","code":404}`)
					case 403:
						_, _ = fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","message":"namespace access denied","code":403}`)
					}
				default:
					t.Errorf("unexpected Kubernetes request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, BearerToken: "credential-must-not-appear"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			info, err := Check(ctx, client)
			if tc.wantError == "" {
				if err != nil || info.GitVersion != "v1.34.1" {
					t.Fatalf("version = %q, error = %v", info.GitVersion, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "credential-must-not-appear") {
				t.Fatalf("expected a safe error, got %v", err)
			} else if tc.wantError == "NotFound" {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("error = %v, want NotFound", err)
				}
			} else if tc.wantError == "Forbidden" {
				if !apierrors.IsForbidden(err) {
					t.Fatalf("error = %v, want Forbidden", err)
				}
			} else if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			wantRequests := []string{"GET /version"}
			if tc.versionCode == 200 && tc.versionBody != "{" {
				wantRequests = append(wantRequests, "GET /api/v1/namespaces/deploy-api")
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(requests, wantRequests) {
				t.Errorf("requests = %v, want %v", requests, wantRequests)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckCancellationAndSharedDeadline(t *testing.T) {
	for _, blockedPath := range []string{"/version", "/api/v1/namespaces/deploy-api"} {
		t.Run(blockedPath, func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == blockedPath {
					close(started)
					<-r.Context().Done()
					close(canceled)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"gitVersion":"v1.34.1"}`)
			}))
			defer upstream.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
				return roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if got, ok := r.Context().Deadline(); !ok || !got.Equal(deadline) {
						t.Errorf("request deadline = %v, want %v", got, deadline)
					}
					return next.RoundTrip(r)
				})
			}})
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { _, err := Check(ctx, client); result <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("upstream request did not start")
			}
			if blockedPath == "/version" {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v, want context cancellation", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("check did not return after cancellation")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream did not observe cancellation")
			}
		})
	}
}

func TestCheckUnreachable(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	address := upstream.URL
	upstream.Close()
	client, err := kubernetes.NewForConfig(&rest.Config{Host: address})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Check(ctx, client); err == nil || !strings.Contains(err.Error(), "get Kubernetes server version") {
		t.Fatalf("error = %v, want version connection failure", err)
	}
}
