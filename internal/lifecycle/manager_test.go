package lifecycle

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"crusoe-cmk-node-remediation/internal/actions"
	"crusoe-cmk-node-remediation/internal/config"
	"crusoe-cmk-node-remediation/internal/constants"
	"crusoe-cmk-node-remediation/internal/crusoe"
	"crusoe-cmk-node-remediation/internal/discovery"
	"crusoe-cmk-node-remediation/internal/guardrails"
	"crusoe-cmk-node-remediation/internal/k8s"
)

func TestRunDryRunNoChanges(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					constants.LabelNodepoolID:   "pool-a",
					constants.LabelInstanceID:   "vm-001",
					constants.LabelProjectID:    "proj-1",
					constants.LabelInstanceType: "b200-180gb-sxm-ib.8x",
				},
			},
			Spec: corev1.NodeSpec{Unschedulable: false},
		},
	)

	k8sClient := k8s.NewClient(clientset, nil)
	cfg := &config.Config{
		CordonDays:           55,
		ActionDays:           60,
		DryRun:               true,
		VMInstanceTypeFilter: "b200.*",
		Action:               config.ActionConfig{Type: "noop", Timeout: 30 * time.Minute, MaxRetries: 3},
		Guardrails:           config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:         10 * time.Minute,
		CrusoeAPISecret:      "test-secret",
		CrusoeProjectID:      "proj-1",
		NodeSelector:         config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	crusoeClient := crusoe.NewClient("http://localhost", "key", "proj-1")
	stepFactory := actions.NewFactory(k8sClient, crusoeClient)

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     k8s.NewDrainManager(k8sClient, clientset, cfg.DrainTimeout, true),
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -30)}, // 30 days ago
		GuardChecker: guardrails.NewChecker(cfg.Guardrails),
		Discoverer:   discovery.NewDiscoverer(k8sClient, cfg),
		Config:       cfg,
		Recorder:     record.NewFakeRecorder(20),
	}

	mgr := NewManager(deps)
	err := mgr.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if node.Spec.Unschedulable {
		t.Error("node should not be cordoned in dry-run with low uptime")
	}
}

func TestRunCordonsNodeAt55Days(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					constants.LabelNodepoolID:   "pool-a",
					constants.LabelInstanceID:   "vm-001",
					constants.LabelProjectID:    "proj-1",
					constants.LabelInstanceType: "b200-180gb-sxm-ib.8x",
				},
			},
			Spec: corev1.NodeSpec{Unschedulable: false},
		},
	)

	k8sClient := k8s.NewClient(clientset, nil)
	cfg := &config.Config{
		CordonDays:           55,
		ActionDays:           60,
		DryRun:               false,
		VMInstanceTypeFilter: "b200.*",
		Action:               config.ActionConfig{Type: "noop", Timeout: 30 * time.Minute, MaxRetries: 3},
		Guardrails:           config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:         10 * time.Minute,
		CrusoeAPISecret:      "test-secret",
		CrusoeProjectID:      "proj-1",
		NodeSelector:         config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	crusoeClient := crusoe.NewClient("http://localhost", "key", "proj-1")
	stepFactory := actions.NewFactory(k8sClient, crusoeClient)

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     k8s.NewDrainManager(k8sClient, clientset, cfg.DrainTimeout, true),
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -56)}, // 56 days ago
		GuardChecker: guardrails.NewChecker(cfg.Guardrails),
		Discoverer:   discovery.NewDiscoverer(k8sClient, cfg),
		Config:       cfg,
		Recorder:     record.NewFakeRecorder(20),
	}

	mgr := NewManager(deps)
	err := mgr.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if !node.Spec.Unschedulable {
		t.Error("node should be cordoned at 56 days uptime")
	}
	if node.Labels[constants.LabelRemediationPhase] != string(constants.PhaseCordoned) {
		t.Errorf("phase = %q, want %q", node.Labels[constants.LabelRemediationPhase], constants.PhaseCordoned)
	}
}

// mockUptimeEvaluator implements UptimeEvaluator for tests.
type mockUptimeEvaluator struct {
	startTime time.Time
}

func (m *mockUptimeEvaluator) GetNodeStartTime(ctx context.Context, nodeName string) (time.Time, error) {
	return m.startTime, nil
}
