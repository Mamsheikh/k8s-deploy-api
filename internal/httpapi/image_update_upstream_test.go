package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-deploy-api/internal/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const imageDeploymentPath = "/apis/apps/v1/namespaces/deploy-api/deployments/demo"
const imageServicePath = "/api/v1/namespaces/deploy-api/services/demo"

func imageUpdateHTTPFixture() (*appsv1.Deployment, *corev1.Service) {
	labels := map[string]string{"app.kubernetes.io/managed-by": "deploy-api", "app.kubernetes.io/name": "demo"}
	one := int32(1)
	d := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: kube.Namespace, UID: "deployment-uid", ResourceVersion: "7", Generation: 3,
			Labels: labels, Annotations: map[string]string{"retained": "deployment"}},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: map[string]string{"retained": "pod"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar", Image: "sidecar:v1"}, {Name: "app", Image: "nginx:v1", ImagePullPolicy: corev1.PullAlways,
				Ports: []corev1.ContainerPort{{ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}}}},
		}},
	}
	s := &corev1.Service{
		TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: kube.Namespace, UID: "service-uid", ResourceVersion: "8", Labels: labels},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.96.0.10", ClusterIPs: []string{"10.96.0.10"}, Selector: labels,
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP}}},
	}
	return d, s
}

func patchImage(t *testing.T, url, image string) (*http.Response, []byte) {
	t.Helper()
	body, err := json.Marshal(kube.ImageUpdateRequest{Image: image})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPatch, url+"/apps/demo/image", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestImageUpdateTypedUpstreamOptimisticConcurrencyAndPreservation(t *testing.T) {
	for _, shape := range []string{"accepted", "identical", "concurrent change", "disappeared"} {
		t.Run(shape, func(t *testing.T) {
			d, s := imageUpdateHTTPFixture()
			originalDeployment, originalService := d.DeepCopy(), s.DeepCopy()
			image := "nginx:v2"
			if shape == "identical" {
				image = "nginx:v1"
			}
			var mu sync.Mutex
			var calls []string
			var deadlines []time.Time
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == imageDeploymentPath:
					_ = json.NewEncoder(w).Encode(d)
				case r.Method == http.MethodGet && r.URL.Path == imageServicePath:
					if shape == "concurrent change" {
						d.ResourceVersion = "9"
						d.Annotations["concurrent"] = "must survive"
					}
					_ = json.NewEncoder(w).Encode(s)
				case r.Method == http.MethodPut && r.URL.Path == imageDeploymentPath:
					var update appsv1.Deployment
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Error(err)
						upstreamStatus(w, 500, metav1.StatusReasonInternalError, "invalid typed update")
						return
					}
					want := originalDeployment.DeepCopy()
					want.Spec.Template.Spec.Containers[1].Image = image
					if !reflect.DeepEqual(&update, want) {
						t.Errorf("upstream received fields beyond the intended image change: %#v", update)
					}
					if shape == "disappeared" {
						upstreamStatus(w, 404, metav1.StatusReasonNotFound, "Deployment was deleted after the preflight reads")
						return
					}
					if update.ResourceVersion != d.ResourceVersion {
						upstreamStatus(w, 409, metav1.StatusReasonConflict, "Deployment changed after it was read")
						return
					}
					update.ResourceVersion, update.Generation = "10", update.Generation+1
					d = &update
					_ = json.NewEncoder(w).Encode(d)
				default:
					t.Errorf("unexpected upstream request %s %s", r.Method, r.URL.Path)
					upstreamStatus(w, 500, metav1.StatusReasonInternalError, "unexpected upstream request")
				}
			}))
			defer upstream.Close()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
				return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("upstream request has no deadline")
					}
					mu.Lock()
					deadlines = append(deadlines, deadline)
					mu.Unlock()
					return next.RoundTrip(r)
				})
			}})
			if err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(NewHandler(kube.Apps{Client: client}))
			defer api.Close()
			start := time.Now()
			response, body := patchImage(t, api.URL, image)
			end := time.Now()
			wantStatus := 202
			if shape == "concurrent change" || shape == "disappeared" {
				wantStatus = 409
			}
			if response.StatusCode != wantStatus {
				t.Fatalf("response %d %s", response.StatusCode, body)
			}
			if wantStatus == 202 {
				if response.Header.Get("Location") != "/apps/demo" || strings.TrimSpace(string(body)) != `{"name":"demo","statusUrl":"/apps/demo"}` {
					t.Fatalf("accepted response %v %s", response.Header, body)
				}
			} else {
				var envelope errorEnvelope
				if err := json.Unmarshal(body, &envelope); err != nil {
					t.Fatal(err)
				}
				if response.Header.Get("Location") != "" || envelope.Error.Code != "conflict" || envelope.Error.Details.Reason != "Conflict" {
					t.Fatalf("conflict response %v %s", response.Header, body)
				}
				if shape == "disappeared" && !strings.Contains(envelope.Error.Message, "Deployment disappeared during image update; inspect before retrying") {
					t.Fatalf("missing race detail %s", body)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			wantCalls := []string{"GET " + imageDeploymentPath, "GET " + imageServicePath}
			if shape != "identical" {
				wantCalls = append(wantCalls, "PUT "+imageDeploymentPath)
			}
			if !reflect.DeepEqual(calls, wantCalls) || len(deadlines) != len(wantCalls) {
				t.Fatalf("upstream calls %v, deadlines %v", calls, deadlines)
			}
			for _, deadline := range deadlines {
				if !deadline.Equal(deadlines[0]) || deadline.Before(start.Add(10*time.Second)) || deadline.After(end.Add(10*time.Second)) {
					t.Fatalf("operation did not share its 10-second deadline: %v", deadlines)
				}
			}
			if !reflect.DeepEqual(s, originalService) {
				t.Fatal("Service changed")
			}
			if shape == "accepted" && (d.Spec.Template.Spec.Containers[1].Image != image || d.Generation != 4) {
				t.Fatalf("accepted image was not stored %#v", d)
			}
			if shape == "identical" && !reflect.DeepEqual(d, originalDeployment) {
				t.Fatal("identical image changed generation or Deployment fields")
			}
			if shape == "concurrent change" && (d.Annotations["concurrent"] != "must survive" || d.Spec.Template.Spec.Containers[1].Image != "nginx:v1") {
				t.Fatalf("conflict overwrote the concurrent state %#v", d)
			}
		})
	}
}

func TestImageUpdateTypedUpstreamErrorMapping(t *testing.T) {
	for _, stage := range []string{"deployment read", "service read", "image write"} {
		for _, tc := range []struct {
			code   int
			reason metav1.StatusReason
			want   int
		}{
			{403, metav1.StatusReasonForbidden, 503},
			{503, metav1.StatusReasonServiceUnavailable, 503},
			{504, metav1.StatusReasonTimeout, 504},
			{500, metav1.StatusReasonInternalError, 500},
		} {
			t.Run(stage+"/"+http.StatusText(tc.code), func(t *testing.T) {
				d, s := imageUpdateHTTPFixture()
				writes := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPut {
						writes++
					}
					if stage == "deployment read" && r.URL.Path == imageDeploymentPath || stage == "service read" && r.URL.Path == imageServicePath || stage == "image write" && r.Method == http.MethodPut {
						upstreamStatus(w, tc.code, tc.reason, "controlled image update failure")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == imageDeploymentPath {
						_ = json.NewEncoder(w).Encode(d)
					} else {
						_ = json.NewEncoder(w).Encode(s)
					}
				}))
				defer upstream.Close()
				client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
				if err != nil {
					t.Fatal(err)
				}
				api := httptest.NewServer(NewHandler(kube.Apps{Client: client}))
				defer api.Close()
				response, body := patchImage(t, api.URL, "nginx:v2")
				var envelope errorEnvelope
				if err := json.Unmarshal(body, &envelope); err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != tc.want || response.Header.Get("Location") != "" || envelope.Error.Message != "controlled image update failure" || envelope.Error.Details.Reason != string(tc.reason) || envelope.Error.Details.Name != "demo" {
					t.Fatalf("response %d %s", response.StatusCode, body)
				}
				if stage != "image write" && writes != 0 || writes > 1 {
					t.Fatalf("rejected update issued %d writes", writes)
				}
			})
		}
	}
}

func TestImageUpdateSharesDeadlineAndCancelsUpstream(t *testing.T) {
	for _, stop := range []string{"deployment read", "service read", "image write"} {
		t.Run(stop, func(t *testing.T) {
			d, s := imageUpdateHTTPFixture()
			canceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if stop == "deployment read" && r.URL.Path == imageDeploymentPath || stop == "service read" && r.URL.Path == imageServicePath || stop == "image write" && r.Method == http.MethodPut {
					<-r.Context().Done()
					close(canceled)
					return
				}
				select {
				case <-time.After(20 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == imageDeploymentPath {
					_ = json.NewEncoder(w).Encode(d)
				} else {
					_ = json.NewEncoder(w).Encode(s)
				}
			}))
			defer upstream.Close()
			var mu sync.Mutex
			var deadlines []time.Time
			var wantDeadline time.Time
			client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL, WrapTransport: func(next http.RoundTripper) http.RoundTripper {
				return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("upstream request has no deadline")
					}
					mu.Lock()
					deadlines = append(deadlines, deadline)
					mu.Unlock()
					return next.RoundTrip(r)
				})
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewHandler(kube.Apps{Client: client})
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
				defer cancel()
				mu.Lock()
				wantDeadline, _ = ctx.Deadline()
				mu.Unlock()
				handler.ServeHTTP(w, r.WithContext(ctx))
			}))
			defer api.Close()
			response, body := patchImage(t, api.URL, "nginx:v2")
			if response.StatusCode != 504 || !strings.Contains(string(body), `"code":"upstream_timeout"`) {
				t.Fatalf("response %d %s", response.StatusCode, body)
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request was not canceled")
			}
			mu.Lock()
			defer mu.Unlock()
			wantCalls := map[string]int{"deployment read": 1, "service read": 2, "image write": 3}[stop]
			if len(deadlines) != wantCalls {
				t.Fatalf("upstream calls %d, want %d", len(deadlines), wantCalls)
			}
			for _, deadline := range deadlines {
				if !deadline.Equal(wantDeadline) {
					t.Fatalf("deadline reset from %v to %v", wantDeadline, deadline)
				}
			}
		})
	}
}

func TestImageUpdateClientCancellationReachesUpstream(t *testing.T) {
	d, s := imageUpdateHTTPFixture()
	entered, canceled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, r.Body)
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == imageDeploymentPath {
			_ = json.NewEncoder(w).Encode(d)
		} else {
			_ = json.NewEncoder(w).Encode(s)
		}
	}))
	defer upstream.Close()
	client, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(NewHandler(kube.Apps{Client: client}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, api.URL+"/apps/demo/image", strings.NewReader(`{"image":"nginx:v2"}`))
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("image write was not issued")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not reach Kubernetes")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("client error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client request did not return after cancellation")
	}
}
