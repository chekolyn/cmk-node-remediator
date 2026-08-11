package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDirectEventRecorderCreateEvent(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	recorder := &directEventRecorder{
		events:    clientset.CoreV1().Events("crusoe-node-remediation"),
		namespace: "crusoe-node-remediation",
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			UID:  "node-uid-123",
		},
	}

	recorder.Eventf(node, corev1.EventTypeNormal, "NodeRemediated", "node remediated with %s", "vm-reset")

	events, err := clientset.CoreV1().Events("crusoe-node-remediation").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list events: %v", err)
	}

	if len(events.Items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events.Items))
	}

	event := events.Items[0]

	// Event should be in the recorder's namespace
	if event.Namespace != "crusoe-node-remediation" {
		t.Errorf("event namespace = %q, want %q", event.Namespace, "crusoe-node-remediation")
	}

	// InvolvedObject namespace must match event namespace (K8s requirement)
	if event.InvolvedObject.Namespace != event.Namespace {
		t.Errorf("InvolvedObject.Namespace = %q, want %q (must match event namespace)",
			event.InvolvedObject.Namespace, event.Namespace)
	}

	// InvolvedObject should reference the node
	if event.InvolvedObject.Kind != "Node" {
		t.Errorf("InvolvedObject.Kind = %q, want %q", event.InvolvedObject.Kind, "Node")
	}
	if event.InvolvedObject.Name != "node-1" {
		t.Errorf("InvolvedObject.Name = %q, want %q", event.InvolvedObject.Name, "node-1")
	}
	if event.InvolvedObject.UID != "node-uid-123" {
		t.Errorf("InvolvedObject.UID = %q, want %q", event.InvolvedObject.UID, "node-uid-123")
	}

	// Event should have the right reason and message
	if event.Reason != "NodeRemediated" {
		t.Errorf("Reason = %q, want %q", event.Reason, "NodeRemediated")
	}
	if event.Message != "node remediated with vm-reset" {
		t.Errorf("Message = %q, want %q", event.Message, "node remediated with vm-reset")
	}
	if event.Type != corev1.EventTypeNormal {
		t.Errorf("Type = %q, want %q", event.Type, corev1.EventTypeNormal)
	}
}

func TestDirectEventRecorderNilEvents(t *testing.T) {
	// Should not panic when events is nil
	recorder := &directEventRecorder{
		events:    nil,
		namespace: "test",
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
	}

	// Should be a no-op, not a panic
	recorder.Eventf(node, corev1.EventTypeNormal, "Test", "test message")
}

func TestDirectEventRecorderNonNodeObject(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	recorder := &directEventRecorder{
		events:    clientset.CoreV1().Events("test"),
		namespace: "test",
	}

	// Pass a non-Node object — should be silently ignored
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
	}
	recorder.Eventf(pod, corev1.EventTypeNormal, "Test", "test message")

	events, _ := clientset.CoreV1().Events("test").List(context.Background(), metav1.ListOptions{})
	if len(events.Items) != 0 {
		t.Errorf("expected 0 events for non-Node object, got %d", len(events.Items))
	}
}
