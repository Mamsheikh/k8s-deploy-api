package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func warning(name string, uid types.UID) *corev1.Event {
	return &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, UID: types.UID("event-" + name)}, Type: corev1.EventTypeWarning, InvolvedObject: corev1.ObjectReference{UID: uid}, Reason: "BackOff", Message: "process exited"}
}

func TestInspectWarningOwnership(t *testing.T) {
	d, s, r, p := readyInspectionFixture()
	s.UID = "service-uid"
	objects := []runtime.Object{d, s, r, p}
	for _, uid := range []types.UID{d.UID, r.UID, p.UID, s.UID, "old-deployment", "old-replicaset", "unrelated", ""} {
		objects = append(objects, warning("warning-"+string(uid), uid))
	}
	normal := warning("normal", p.UID)
	normal.Type = corev1.EventTypeNormal
	other := warning("another-namespace", p.UID)
	other.Namespace = "default"
	objects = append(objects, normal, other)
	got, err := (Apps{Client: fake.NewClientset(objects...)}).Inspect(context.Background(), "demo")
	if err != nil || !got.Ready || got.DiagnosticsIncomplete || got.DiagnosticError != nil {
		t.Fatalf("inspection %#v %v", got, err)
	}
	if len(got.WarningEvents) != 3 {
		t.Fatalf("warnings %#v", got.WarningEvents)
	}
	want := map[types.UID]bool{d.UID: true, r.UID: true, p.UID: true}
	for _, event := range got.WarningEvents {
		if !want[event.InvolvedObject.UID] || event.Reason != "BackOff" || event.Message != "process exited" || event.Timestamp != nil {
			t.Fatalf("warning %#v", event)
		}
		delete(want, event.InvolvedObject.UID)
	}
	if len(want) != 0 {
		t.Fatalf("missing UIDs %v", want)
	}
}

func TestWarningNewestTen(t *testing.T) {
	objects := []runtime.Object{}
	for i := 0; i < 20; i++ {
		event := warning(fmt.Sprintf("warning-%02d", i), "pod")
		event.LastTimestamp = metav1.NewTime(time.Unix(int64(i+1), 0))
		objects = append(objects, event)
	}
	for i := 0; i < 2; i++ {
		event := warning(fmt.Sprintf("no-time-%d", i), "pod")
		objects = append(objects, event)
	}
	client := fake.NewClientset(objects...)
	got, err := (Apps{Client: client}).warningEvents(context.Background(), map[types.UID]struct{}{"pod": {}})
	if err != nil || len(got) != 10 {
		t.Fatalf("warnings %#v %v", got, err)
	}
	for i, event := range got {
		if event.Timestamp == nil || event.Timestamp.Unix() != int64(20-i) {
			t.Fatalf("position %d event %#v", i, event)
		}
	}
}

func TestWarningTimestampFallbacksAndTies(t *testing.T) {
	at := func(n int64) metav1.Time { return metav1.NewTime(time.Unix(n, 0)) }
	series := warning("series", "pod")
	series.Series = &corev1.EventSeries{LastObservedTime: metav1.NewMicroTime(at(6).Time)}
	series.LastTimestamp = at(1)
	last := warning("last", "pod")
	last.LastTimestamp = at(5)
	last.EventTime = metav1.NewMicroTime(at(1).Time)
	event := warning("event", "pod")
	event.EventTime = metav1.NewMicroTime(at(4).Time)
	event.FirstTimestamp = at(1)
	first := warning("first", "pod")
	first.FirstTimestamp = at(3)
	first.CreationTimestamp = at(1)
	created := warning("created", "pod")
	created.CreationTimestamp = at(2)
	a, b := warning("a", "pod"), warning("b", "pod")
	for _, e := range []*corev1.Event{series, last, event, first, created, a, b} {
		e.Message = e.Name
	}
	client := fake.NewClientset(b, first, a, created, event, last, series)
	for run := 0; run < 3; run++ {
		got, err := (Apps{Client: client}).warningEvents(context.Background(), map[types.UID]struct{}{"pod": {}})
		if err != nil || len(got) != 7 {
			t.Fatalf("warnings %#v %v", got, err)
		}
		for i, name := range []string{"series", "last", "event", "first", "created", "a", "b"} {
			if got[i].Message != name {
				t.Fatalf("order %#v", got)
			}
			if i < 5 && (got[i].Timestamp == nil || got[i].Timestamp.Unix() != int64(6-i)) {
				t.Fatalf("timestamp %#v", got[i])
			}
			if i >= 5 && got[i].Timestamp != nil {
				t.Fatal("invented timestamp")
			}
		}
	}
}

func TestInspectEventFailureKeepsCore(t *testing.T) {
	for _, failure := range []error{apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("denied")), context.DeadlineExceeded, errors.New("secret transport credentials")} {
		d, s, r, p := readyInspectionFixture()
		client := fake.NewClientset(d, s, r, p)
		client.PrependReactor("list", "events", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, failure })
		got, err := (Apps{Client: client}).Inspect(context.Background(), "demo")
		if err != nil || !got.Ready || !got.DiagnosticsIncomplete || got.DiagnosticError == nil || got.WarningEvents == nil || len(got.Pods) != 1 {
			t.Fatalf("inspection %#v %v", got, err)
		}
		if strings.Contains(got.DiagnosticError.Message, "secret") {
			t.Fatal("transport secret leaked")
		}
	}
}

func TestInspectServiceOnlySkipsUnrelatedWarnings(t *testing.T) {
	s := service(AppSpec{Name: "demo"})
	client := fake.NewClientset(s, warning("service", s.UID))
	got, err := (Apps{Client: client}).Inspect(context.Background(), "demo")
	if err != nil || got.WarningEvents == nil || len(got.WarningEvents) != 0 || got.DiagnosticsIncomplete {
		t.Fatalf("inspection %#v %v", got, err)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "events" {
			t.Fatal("Service warnings are outside the ownership graph")
		}
	}
}

func TestInspectCrashDiagnosticsWithWarnings(t *testing.T) {
	d, s, r, p := readyInspectionFixture()
	d.Status.AvailableReplicas = 0
	d.Status.ReadyReplicas = 0
	p.Status.Conditions = nil
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", RestartCount: 3, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}, LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}}}}
	got, err := (Apps{Client: fake.NewClientset(d, s, r, p, warning("backoff", p.UID))}).Inspect(context.Background(), "demo")
	if err != nil || got.Ready || len(got.WarningEvents) != 1 {
		t.Fatalf("inspection %#v %v", got, err)
	}
	container := got.Pods[0].Container
	if container.RestartCount != 3 || container.State.Waiting.Reason != "CrashLoopBackOff" || container.LastTerminationState.Terminated.ExitCode != 1 {
		t.Fatalf("container %#v", container)
	}
}
