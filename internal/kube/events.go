package kube

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type WarningEvent struct {
	InvolvedObject corev1.ObjectReference `json:"involvedObject"`
	Reason         string                 `json:"reason,omitempty"`
	Message        string                 `json:"message,omitempty"`
	Timestamp      *metav1.Time           `json:"timestamp,omitempty"`
}

func (a Apps) warningEvents(ctx context.Context, uids map[types.UID]struct{}) ([]WarningEvent, error) {
	result := []WarningEvent{}
	events, err := a.Client.CoreV1().Events(Namespace).List(ctx, metav1.ListOptions{FieldSelector: "type=Warning"})
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	filtered := make([]corev1.Event, 0)
	for _, event := range events.Items {
		_, owned := uids[event.InvolvedObject.UID]
		if owned && event.InvolvedObject.UID != "" && event.Type == corev1.EventTypeWarning && event.Namespace == Namespace {
			filtered = append(filtered, event)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		a, b := eventTimestamp(filtered[i]), eventTimestamp(filtered[j])
		if !a.Equal(&b) {
			return a.After(b.Time)
		}
		if filtered[i].Name != filtered[j].Name {
			return filtered[i].Name < filtered[j].Name
		}
		return filtered[i].UID < filtered[j].UID
	})
	if len(filtered) > 10 {
		filtered = filtered[:10]
	}
	for _, event := range filtered {
		diagnostic := WarningEvent{InvolvedObject: event.InvolvedObject, Reason: event.Reason, Message: event.Message}
		if timestamp := eventTimestamp(event); !timestamp.IsZero() {
			diagnostic.Timestamp = &timestamp
		}
		result = append(result, diagnostic)
	}
	return result, nil
}

func eventTimestamp(event corev1.Event) metav1.Time {
	if event.Series != nil && !event.Series.LastObservedTime.IsZero() {
		return metav1.NewTime(event.Series.LastObservedTime.Time)
	}
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp
	}
	if !event.EventTime.IsZero() {
		return metav1.NewTime(event.EventTime.Time)
	}
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp
	}
	return event.CreationTimestamp
}
