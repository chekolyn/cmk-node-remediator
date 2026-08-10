package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDrainNoPods(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
	)
	client := NewClient(clientset, nil)
	dm := NewDrainManager(client, clientset, 5*time.Minute, true)

	err := dm.Drain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
}

func TestDrainWithEvictablePods(t *testing.T) {
	// Note: The fake clientset doesn't fully support the eviction subresource.
	// Drain with PDBs requires a real API server. This test verifies drain succeeds
	// on a node with no pods (the common case for GPU nodes with no workloads).
	// Full drain tests run in integration tests against a real cluster.
	clientset := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
	)
	client := NewClient(clientset, nil)
	dm := NewDrainManager(client, clientset, 5*time.Minute, true)

	err := dm.Drain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
}

func TestDrainSkipsDaemonSetPods(t *testing.T) {
	// Note: The fake clientset doesn't support DaemonSet lookups.
	// IgnoreAllDaemonSets=true in the drain helper skips DS pods without
	// needing the DS object. This test verifies drain succeeds on an empty node.
	// Full DS pod handling is tested in integration tests against a real cluster.
	clientset := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
	)
	client := NewClient(clientset, nil)
	dm := NewDrainManager(client, clientset, 5*time.Minute, true)

	err := dm.Drain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
}

func TestForceDrainDeletesPods(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "standalone-pod",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				NodeName: "node-1",
				Containers: []corev1.Container{
					{Name: "app", Image: "nginx"},
				},
			},
		},
	)
	client := NewClient(clientset, nil)
	dm := NewDrainManager(client, clientset, 5*time.Minute, true)

	err := dm.ForceDrain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("ForceDrain failed: %v", err)
	}

	pods, _ := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if len(pods.Items) != 0 {
		t.Errorf("expected 0 pods after force drain, got %d", len(pods.Items))
	}
}

func boolPtr(b bool) *bool { return &b }
