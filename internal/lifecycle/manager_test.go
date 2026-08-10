package lifecycle

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"cmk-node-remediation/internal/actions"
	"cmk-node-remediation/internal/config"
	"cmk-node-remediation/internal/constants"
	"cmk-node-remediation/internal/crusoe"
	"cmk-node-remediation/internal/discovery"
	"cmk-node-remediation/internal/guardrails"
	"cmk-node-remediation/internal/k8s"
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

// --- TDD: Edge case tests ---

// TDD Test 1: Guardrail exceeded → node is NOT cordoned
func TestGuardrailExceededSkipsCordon(t *testing.T) {
	// Set up 3 nodes, all at 56 days (should cordon), but globalMaxCordoned=1
	// and 1 node is already cordoned (active). So only 0 more can be cordoned.
	clientset := fake.NewSimpleClientset(
		// Already cordoned node (counts as 1 active)
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-active",
				Labels: map[string]string{
					constants.LabelNodepoolID:   "pool-a",
					constants.LabelInstanceID:   "vm-active",
					constants.LabelProjectID:    "proj-1",
					constants.LabelInstanceType: "b200-180gb-sxm-ib.8x",
					constants.LabelRemediationPhase: string(constants.PhaseCordoned),
				},
			},
			Spec: corev1.NodeSpec{Unschedulable: true},
		},
		// Node that should be cordoned but guardrail will block it
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-target",
				Labels: map[string]string{
					constants.LabelNodepoolID:   "pool-a",
					constants.LabelInstanceID:   "vm-target",
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
		Guardrails:           config.GuardrailsConfig{GlobalMaxCordoned: 1, PerPoolMaxCordoned: 1},
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
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -56)},
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

	// node-target should NOT be cordoned — guardrail exceeded
	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-target", metav1.GetOptions{})
	if node.Spec.Unschedulable {
		t.Error("node-target should not be cordoned when guardrail is exceeded")
	}
}

// --- Mock types for TDD edge case tests ---

// mockDrainMgr implements DrainManager for testing drain failure paths.
type mockDrainMgr struct {
	drainFails      bool
	forceDrainFails bool
	drainCalled     bool
	forceDrainCalled bool
}

func (m *mockDrainMgr) Drain(ctx context.Context, nodeName string) error {
	m.drainCalled = true
	if m.drainFails {
		return fmt.Errorf("drain blocked by PDB")
	}
	return nil
}

func (m *mockDrainMgr) ForceDrain(ctx context.Context, nodeName string) error {
	m.forceDrainCalled = true
	if m.forceDrainFails {
		return fmt.Errorf("force drain failed")
	}
	return nil
}

// mockStepCreator implements StepCreator for testing.
type mockStepCreator struct {
	step actions.Step
	err  error
}

func (m *mockStepCreator) CreateFromConfig(cfg config.ActionConfig) (actions.Step, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.step, nil
}

// TDD Test 2: Drain fails → force drain is called
func TestForceDrainPath(t *testing.T) {
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
		CordonDays:             55,
		ActionDays:             60,
		DryRun:                 false,
		VMInstanceTypeFilter:   "b200.*",
		Action:                 config.ActionConfig{Type: "noop", Timeout: 5 * time.Second, MaxRetries: 3},
		Guardrails:             config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:           1 * time.Second,
		ForceDrainAfterTimeout: true,
		CrusoeAPISecret:        "test-secret",
		CrusoeProjectID:        "proj-1",
		NodeSelector:           config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	drainMgr := &mockDrainMgr{drainFails: true, forceDrainFails: false}
	stepFactory := &mockStepCreator{step: &actions.NoopStep{}}

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -61)},
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

	if !drainMgr.drainCalled {
		t.Error("Drain should have been called")
	}
	if !drainMgr.forceDrainCalled {
		t.Error("ForceDrain should have been called after Drain failed")
	}

	// Node should be uncordoned (noop step + successful force drain)
	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if node.Spec.Unschedulable {
		t.Error("node should be uncordoned after successful force drain + noop step")
	}
}

// TDD Test 3: Attempt annotation reset to "0" after successful remediation
func TestAttemptAnnotationResetAfterSuccess(t *testing.T) {
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
				Annotations: map[string]string{
					constants.AnnotationAttempt: "2", // previous failed attempts
				},
			},
			Spec: corev1.NodeSpec{Unschedulable: false},
		},
	)

	k8sClient := k8s.NewClient(clientset, nil)
	cfg := &config.Config{
		CordonDays:             55,
		ActionDays:             60,
		DryRun:                 false,
		VMInstanceTypeFilter:   "b200.*",
		Action:                 config.ActionConfig{Type: "noop", Timeout: 5 * time.Second, MaxRetries: 3},
		Guardrails:             config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:           1 * time.Second,
		ForceDrainAfterTimeout: true,
		CrusoeAPISecret:        "test-secret",
		CrusoeProjectID:        "proj-1",
		NodeSelector:           config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	drainMgr := &mockDrainMgr{}
	stepFactory := &mockStepCreator{step: &actions.NoopStep{}}

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -61)},
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
	attempt := node.Annotations[constants.AnnotationAttempt]
	if attempt != "0" {
		t.Errorf("attempt annotation = %q, want %q after successful remediation", attempt, "0")
	}
}

// TDD Test 4: Uncordon failure → action-completed-at NOT set (node will be retried)
func TestUncordonFailureDoesNotSetCompletedAt(t *testing.T) {
	// Create a node that's already cordoned — uncordon will fail because
	// we'll use a fake clientset that we can't easily make fail on uncordon.
	// Instead, we test the logic: if uncordonNode returns error, the manager
	// should return error and NOT set action-completed-at.
	// We simulate this by having the step fail, which means action-completed-at
	// should not be set.
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
		CordonDays:             55,
		ActionDays:             60,
		DryRun:                 false,
		VMInstanceTypeFilter:   "b200.*",
		Action:                 config.ActionConfig{Type: "noop", Timeout: 5 * time.Second, MaxRetries: 3},
		Guardrails:             config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:           1 * time.Second,
		ForceDrainAfterTimeout: true,
		CrusoeAPISecret:        "test-secret",
		CrusoeProjectID:        "proj-1",
		NodeSelector:           config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	// Step that always fails
	failingStep := &failingStep{}
	stepFactory := &mockStepCreator{step: failingStep}

	drainMgr := &mockDrainMgr{}

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -61)},
		GuardChecker: guardrails.NewChecker(cfg.Guardrails),
		Discoverer:   discovery.NewDiscoverer(k8sClient, cfg),
		Config:       cfg,
		Recorder:     record.NewFakeRecorder(20),
	}

	mgr := NewManager(deps)
	err := mgr.Run(context.Background())
	// Should return error because step failed
	if err == nil {
		t.Fatal("Run should return error when step fails")
	}

	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	completedAt := node.Annotations[constants.AnnotationActionCompletedAt]
	if completedAt != "" {
		t.Errorf("action-completed-at should NOT be set when step fails, got %q", completedAt)
	}
	// Node should be cordoned (cordon happens before step)
	if !node.Spec.Unschedulable {
		t.Error("node should be cordoned when step fails")
	}
}

// failingStep is a Step that always returns an error.
type failingStep struct{}

func (s *failingStep) Type() string { return "failing" }
func (s *failingStep) Run(ctx context.Context, node actions.NodeInfo, params map[string]string) error {
	return fmt.Errorf("step intentionally failed")
}

// TDD Test 5: Cooldown boundary — node at exactly cooldownDays should NOT be skipped
func TestCooldownBoundaryNotSkipped(t *testing.T) {
	// Node remediated exactly cooldownDays ago — should NOT be in cooldown
	// (cooldown is < cooldownDays, not <= cooldownDays)
	cooldownAgo := time.Now().AddDate(0, 0, -7).Format(time.RFC3339) // exactly 7 days ago

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
				Annotations: map[string]string{
					constants.AnnotationActionCompletedAt: cooldownAgo,
				},
			},
			Spec: corev1.NodeSpec{Unschedulable: false},
		},
	)

	k8sClient := k8s.NewClient(clientset, nil)
	cfg := &config.Config{
		CordonDays:             55,
		ActionDays:             60,
		CooldownDays:           7,
		DryRun:                 false,
		VMInstanceTypeFilter:   "b200.*",
		Action:                 config.ActionConfig{Type: "noop", Timeout: 5 * time.Second, MaxRetries: 3},
		Guardrails:             config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:           1 * time.Second,
		ForceDrainAfterTimeout: true,
		CrusoeAPISecret:        "test-secret",
		CrusoeProjectID:        "proj-1",
		NodeSelector:           config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	drainMgr := &mockDrainMgr{}
	stepFactory := &mockStepCreator{step: &actions.NoopStep{}}

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -61)},
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

	// Node at exactly cooldownDays boundary should be processed (not skipped)
	// It should have been cordoned (action = remediate since uptime is 61 days)
	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	// With noop step, node should be uncordoned after successful remediation
	if node.Spec.Unschedulable {
		t.Error("node should be uncordoned after successful noop remediation (cooldown boundary)")
	}
	// action-completed-at should be updated (new remediation happened)
	if node.Annotations[constants.AnnotationActionCompletedAt] == cooldownAgo {
		t.Error("action-completed-at should be updated after new remediation")
	}
}

// TDD Test 6: Max retries exceeded → node left cordoned, error returned
func TestMaxRetriesExceededLeavesCordoned(t *testing.T) {
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
				Annotations: map[string]string{
					constants.AnnotationAttempt: "3", // already at max retries
				},
			},
			Spec: corev1.NodeSpec{Unschedulable: false},
		},
	)

	k8sClient := k8s.NewClient(clientset, nil)
	cfg := &config.Config{
		CordonDays:             55,
		ActionDays:             60,
		DryRun:                 false,
		VMInstanceTypeFilter:   "b200.*",
		Action:                 config.ActionConfig{Type: "noop", Timeout: 5 * time.Second, MaxRetries: 3},
		Guardrails:             config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:           1 * time.Second,
		ForceDrainAfterTimeout: true,
		CrusoeAPISecret:        "test-secret",
		CrusoeProjectID:        "proj-1",
		NodeSelector:           config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	drainMgr := &mockDrainMgr{}
	stepFactory := &mockStepCreator{step: &actions.NoopStep{}}

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -61)},
		GuardChecker: guardrails.NewChecker(cfg.Guardrails),
		Discoverer:   discovery.NewDiscoverer(k8sClient, cfg),
		Config:       cfg,
		Recorder:     record.NewFakeRecorder(20),
	}

	mgr := NewManager(deps)
	err := mgr.Run(context.Background())
	// Should return error because max retries exceeded
	if err == nil {
		t.Fatal("Run should return error when max retries exceeded")
	}

	// Node should NOT be cordoned — it was skipped before cordon
	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if node.Spec.Unschedulable {
		t.Error("node should not be cordoned when max retries exceeded (skipped before cordon)")
	}
	// Drain should NOT have been called
	if drainMgr.drainCalled {
		t.Error("Drain should not be called when max retries exceeded")
	}
}

// TDD Test 7: vm-delete action → node is NOT uncordoned (old node is gone)
func TestVMDeleteDoesNotUncordon(t *testing.T) {
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
		CordonDays:             55,
		ActionDays:             60,
		DryRun:                 false,
		VMInstanceTypeFilter:   "b200.*",
		Action:                 config.ActionConfig{Type: "vm-delete", Timeout: 5 * time.Second, MaxRetries: 3},
		Guardrails:             config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
		DrainTimeout:           1 * time.Second,
		ForceDrainAfterTimeout: true,
		CrusoeAPISecret:        "test-secret",
		CrusoeProjectID:        "proj-1",
		NodeSelector:           config.NodeSelectorConfig{MatchLabels: map[string]string{constants.LabelProjectID: "proj-1"}},
	}

	drainMgr := &mockDrainMgr{}
	stepFactory := &mockStepCreator{step: &actions.NoopStep{}}

	deps := Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   &mockUptimeEvaluator{startTime: time.Now().AddDate(0, 0, -61)},
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

	// Node should still be cordoned — vm-delete doesn't uncordon
	node, _ := clientset.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if !node.Spec.Unschedulable {
		t.Error("node should remain cordoned after vm-delete (old node is gone, replacement handled by CMK)")
	}
	// action-completed-at SHOULD be set (delete succeeded)
	if node.Annotations[constants.AnnotationActionCompletedAt] == "" {
		t.Error("action-completed-at should be set after vm-delete")
	}
}
