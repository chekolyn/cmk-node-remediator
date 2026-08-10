package actions

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"cmk-node-remediation/internal/actions/helpers"
	"cmk-node-remediation/internal/crusoe"
	"cmk-node-remediation/internal/k8s"
)

func TestNoopStep(t *testing.T) {
	step := &NoopStep{}
	nodeInfo := NodeInfo{Name: "node-1", InstanceID: "vm-001"}

	if step.Type() != "noop" {
		t.Errorf("Type() = %q, want %q", step.Type(), "noop")
	}

	if err := step.Run(context.Background(), nodeInfo, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestVMResetStepType(t *testing.T) {
	step := &VMResetStep{}
	if step.Type() != "vm-reset" {
		t.Errorf("Type() = %q, want %q", step.Type(), "vm-reset")
	}
}

func TestVMResetStepRequiresNode(t *testing.T) {
	// VMResetStep should NOT skip based on Ready status — it always proceeds.
	// The cooldown check in the lifecycle manager handles "already remediated".
	// This test verifies the step fails gracefully if the node doesn't exist.
	clientset := fake.NewSimpleClientset() // no nodes
	k8sClient := k8s.NewClient(clientset, nil)
	crusoeClient := crusoe.NewClient("http://localhost", "key", "proj-1")

	step := &VMResetStep{
		crusoeClient: crusoeClient,
		k8sClient:    k8sClient,
		bootVerifier: helpers.NewBootVerifier(k8sClient),
		timeout:      5 * time.Second,
		maxRetries:   3,
	}

	nodeInfo := NodeInfo{Name: "nonexistent-node", InstanceID: "vm-001"}
	err := step.Run(context.Background(), nodeInfo, nil)
	if err == nil {
		t.Fatal("Run should fail when node doesn't exist")
	}
}

func TestFactoryCreatesSingleStep(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	k8sClient := k8s.NewClient(clientset, nil)
	crusoeClient := crusoe.NewClient("http://localhost", "key", "proj-1")

	factory := NewFactory(k8sClient, crusoeClient)
	step, err := factory.Create("noop", 30*time.Second, 3)
	if err != nil {
		t.Fatalf("Create(noop) failed: %v", err)
	}
	if step.Type() != "noop" {
		t.Errorf("step type = %q, want noop", step.Type())
	}
}

func TestFactoryCreatesInvalidType(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	k8sClient := k8s.NewClient(clientset, nil)
	crusoeClient := crusoe.NewClient("http://localhost", "key", "proj-1")

	factory := NewFactory(k8sClient, crusoeClient)
	_, err := factory.Create("invalid", 30*time.Second, 3)
	if err == nil {
		t.Fatal("expected error for invalid step type")
	}
}

func TestStepsComposite(t *testing.T) {
	noop1 := &NoopStep{}
	noop2 := &NoopStep{}

	steps := &Steps{
		steps: []Step{noop1, noop2},
	}

	if steps.Type() != "steps" {
		t.Errorf("Type() = %q, want steps", steps.Type())
	}

	err := steps.Run(context.Background(), NodeInfo{Name: "node-1"}, nil)
	if err != nil {
		t.Fatalf("Steps.Run failed: %v", err)
	}
}

func TestBootVerifierCaptureBootID(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				NodeInfo: corev1.NodeSystemInfo{BootID: "boot-abc"},
			},
		},
	)
	k8sClient := k8s.NewClient(clientset, nil)
	bv := helpers.NewBootVerifier(k8sClient)

	bootID, err := bv.CaptureBootID(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("CaptureBootID failed: %v", err)
	}
	if bootID != "boot-abc" {
		t.Errorf("bootID = %q, want boot-abc", bootID)
	}
}
