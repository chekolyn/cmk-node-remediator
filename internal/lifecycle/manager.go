package lifecycle

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	"cmk-node-remediation/internal/actions"
	"cmk-node-remediation/internal/config"
	"cmk-node-remediation/internal/constants"
	"cmk-node-remediation/internal/discovery"
	"cmk-node-remediation/internal/guardrails"
	"cmk-node-remediation/internal/k8s"
)

// UptimeEvaluator interface for querying VM uptime.
type UptimeEvaluator interface {
	GetNodeStartTime(ctx context.Context, nodeName string) (time.Time, error)
}

// Dependencies holds all the components the manager needs.
type Dependencies struct {
	K8sClient    *k8s.Client
	DrainMgr     *k8s.DrainManager
	StepFactory  *actions.Factory
	UptimeEval   UptimeEvaluator
	GuardChecker *guardrails.Checker
	Discoverer   *discovery.Discoverer
	Config       *config.Config
	Recorder     record.EventRecorder
}

// Manager orchestrates the node lifecycle.
type Manager struct {
	deps Dependencies
}

func NewManager(deps Dependencies) *Manager {
	return &Manager{deps: deps}
}

// Run executes one full lifecycle pass.
func (m *Manager) Run(ctx context.Context) error {
	log.Println("starting lifecycle run")

	groups, err := m.deps.Discoverer.Discover(ctx)
	if err != nil {
		return fmt.Errorf("node discovery failed: %w", err)
	}

	if len(groups) == 0 {
		log.Println("no target nodes found")
		return nil
	}

	log.Printf("discovered %d nodepools with %d total nodes",
		len(groups), m.totalNodeCount(groups))

	// Count currently active nodes for guardrails
	globalActive := m.countActiveNodes(ctx)
	globalTotal := m.countTotalNodes(ctx)

	var failedCount int

	for _, group := range groups {
		poolActive := m.countActiveNodesInPool(ctx, group.NodepoolID)
		poolTotal := len(group.Nodes)

		for _, nodeInfo := range group.Nodes {
			// Query uptime from K8s stats summary API
			startTime, err := m.deps.UptimeEval.GetNodeStartTime(ctx, nodeInfo.Name)
			if err != nil {
				log.Printf("skipping node %s: failed to get uptime: %v", nodeInfo.Name, err)
				continue
			}

			days := time.Since(startTime).Hours() / 24.0

			// Check cooldown — skip if remediated recently
			if m.isInCooldown(nodeInfo.Node) {
				log.Printf("skipping node %s: in cooldown (remediated within %d days)", nodeInfo.Name, m.deps.Config.CooldownDays)
				continue
			}

			action := ClassifyUptime(days, m.deps.Config.CordonDays, m.deps.Config.ActionDays)

			switch action {
			case ActionNone:
				m.handleMonitoredNode(ctx, nodeInfo)
			case ActionCordon:
				m.handleCordonNode(ctx, nodeInfo, days, &globalActive, &poolActive, globalTotal, poolTotal)
			case ActionRemediate:
				if err := m.handleRemediateNode(ctx, nodeInfo, days, &globalActive, &poolActive, globalTotal, poolTotal); err != nil {
					failedCount++
				}
			}
		}
	}

	log.Println("lifecycle run complete")

	if failedCount > 0 {
		return fmt.Errorf("%d nodes failed remediation", failedCount)
	}

	return nil
}

func (m *Manager) isInCooldown(node *corev1.Node) bool {
	completedAt := node.Annotations[constants.AnnotationActionCompletedAt]
	if completedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, completedAt)
	if err != nil {
		return false
	}
	cooldownDuration := time.Duration(m.deps.Config.CooldownDays) * 24 * time.Hour
	return time.Since(t) < cooldownDuration
}

func (m *Manager) handleMonitoredNode(ctx context.Context, nodeInfo discovery.NodeInfo) {
	node := nodeInfo.Node

	if node.Labels[constants.LabelRemediationManaged] != "true" {
		return
	}
	if !k8s.IsCordoned(node) {
		return
	}

	if m.deps.Config.DryRun {
		log.Printf("[dry-run] would uncordon node %s (uptime < %d days)",
			nodeInfo.Name, m.deps.Config.CordonDays)
		return
	}

	log.Printf("uncordoning node %s (uptime below threshold)", nodeInfo.Name)
	if err := m.deps.K8sClient.Uncordon(ctx, nodeInfo.Name); err != nil {
		log.Printf("ERROR: failed to uncordon node %s: %v", nodeInfo.Name, err)
		m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeWarning, "UncordonFailed", "failed to uncordon: %v", err)
		return
	}
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseMonitored))
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintScheduled)
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintDraining)
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintMaintenance)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationUncordonedAt, time.Now().UTC().Format(time.RFC3339))
	m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeNormal, "NodeUncordoned", "node uncordoned (uptime below threshold)")
}

func (m *Manager) handleCordonNode(ctx context.Context, nodeInfo discovery.NodeInfo, days float64, globalActive, poolActive *int, globalTotal, poolTotal int) {
	if !m.deps.GuardChecker.CanCordon(*globalActive, *poolActive, globalTotal, poolTotal) {
		log.Printf("skipping node %s: guardrail exceeded (global=%d/%d, pool=%d/%d)",
			nodeInfo.Name, *globalActive, globalTotal, *poolActive, poolTotal)
		return
	}

	if m.deps.Config.DryRun {
		log.Printf("[dry-run] would cordon node %s (uptime %.1f days)", nodeInfo.Name, days)
		return
	}

	log.Printf("cordoning node %s (uptime %.1f days)", nodeInfo.Name, days)

	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationManaged, "true")

	if err := m.deps.K8sClient.Cordon(ctx, nodeInfo.Name); err != nil {
		log.Printf("ERROR: failed to cordon node %s: %v", nodeInfo.Name, err)
		return
	}

	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseCordoned))
	m.deps.K8sClient.AddTaint(ctx, nodeInfo.Name, constants.TaintScheduled)

	now := time.Now().UTC().Format(time.RFC3339)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationCordonedAt, now)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationCordonUptime, fmt.Sprintf("%.1fd", days))
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationManagedBy, constants.ManagedByValue)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationLastAction, "cordon")
	m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeNormal, "NodeCordoned", "node cordoned at %.1f days uptime", days)

	// Only increment for newly cordoned nodes
	*globalActive++
	*poolActive++
}

func (m *Manager) handleRemediateNode(ctx context.Context, nodeInfo discovery.NodeInfo, days float64, globalActive, poolActive *int, globalTotal, poolTotal int) error {
	if !m.deps.GuardChecker.CanCordon(*globalActive, *poolActive, globalTotal, poolTotal) {
		log.Printf("skipping node %s: guardrail exceeded (global=%d/%d, pool=%d/%d)",
			nodeInfo.Name, *globalActive, globalTotal, *poolActive, poolTotal)
		return nil
	}

	if m.deps.Config.DryRun {
		log.Printf("[dry-run] would remediate node %s with action %s (uptime %.1f days)",
			nodeInfo.Name, m.deps.Config.Action.Type, days)
		return nil
	}

	log.Printf("remediating node %s with action %s (uptime %.1f days)",
		nodeInfo.Name, m.deps.Config.Action.Type, days)

	// Check cross-run retry limit via attempt annotation
	attempt := getAttemptAnnotation(nodeInfo.Node)
	if attempt >= m.deps.Config.Action.MaxRetries {
		log.Printf("node %s exceeded max retries (%d), leaving cordoned", nodeInfo.Name, attempt)
		m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeWarning, "MaxRetriesExceeded",
			"remediation failed after %d attempts, manual intervention required", attempt)
		return fmt.Errorf("node %s exceeded max retries", nodeInfo.Name)
	}
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationAttempt, strconv.Itoa(attempt+1))

	// Set managed label
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationManaged, "true")

	// 1. Cordon
	if err := m.deps.K8sClient.Cordon(ctx, nodeInfo.Name); err != nil {
		log.Printf("ERROR: failed to cordon node %s: %v", nodeInfo.Name, err)
		return err
	}
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseCordoned))
	m.deps.K8sClient.AddTaint(ctx, nodeInfo.Name, constants.TaintScheduled)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationCordonedAt, time.Now().UTC().Format(time.RFC3339))

	// 2. Drain
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseDraining))
	m.deps.K8sClient.AddTaint(ctx, nodeInfo.Name, constants.TaintDraining)
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintScheduled)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationDrainStartedAt, time.Now().UTC().Format(time.RFC3339))

	drainErr := m.deps.DrainMgr.Drain(ctx, nodeInfo.Name)
	if drainErr != nil {
		log.Printf("drain blocked for node %s: %v", nodeInfo.Name, drainErr)
		m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeWarning, "DrainBlocked", "drain blocked: %v", drainErr)
		if m.deps.Config.ForceDrainAfterTimeout {
			log.Printf("force draining node %s", nodeInfo.Name)
			m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseForceDraining))
			m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationForceDrainStartedAt, time.Now().UTC().Format(time.RFC3339))
			if err := m.deps.DrainMgr.ForceDrain(ctx, nodeInfo.Name); err != nil {
				log.Printf("ERROR: force drain failed for node %s: %v", nodeInfo.Name, err)
				m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeWarning, "ForceDrainFailed", "force drain failed: %v", err)
				return err
			}
		} else {
			log.Printf("skipping node %s: drain blocked and force drain disabled", nodeInfo.Name)
			return drainErr
		}
	}

	// 3. Execute remediation step(s)
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseActionRunning))
	m.deps.K8sClient.AddTaint(ctx, nodeInfo.Name, constants.TaintMaintenance)
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintDraining)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationActionTriggeredAt, time.Now().UTC().Format(time.RFC3339))
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationActionType, m.deps.Config.Action.Type)

	// Build step from config
	step, err := m.deps.StepFactory.CreateFromConfig(m.deps.Config.Action)
	if err != nil {
		log.Printf("ERROR: failed to create step for node %s: %v", nodeInfo.Name, err)
		m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeWarning, "StepCreationFailed", "failed to create step: %v", err)
		return err
	}

	actNode := actions.NodeInfo{
		Name:       nodeInfo.Name,
		InstanceID: nodeInfo.InstanceID,
	}

	if err := step.Run(ctx, actNode, m.deps.Config.Action.Params); err != nil {
		log.Printf("ERROR: step %s failed for node %s: %v", step.Type(), nodeInfo.Name, err)
		m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeWarning, "StepFailed",
			"step %s failed (attempt %d): %v", step.Type(), attempt+1, err)
		// Leave node cordoned + tainted, next CronJob run will retry
		return err
	}

	// 4. Uncordon (skip for vm-delete — old node is gone)
	if m.deps.Config.Action.Type != "vm-delete" && len(m.deps.Config.Action.Steps) == 0 {
		if err := m.uncordonNode(ctx, nodeInfo, step.Type(), days); err != nil {
			// Uncordon failed — don't set action-completed-at, so cooldown
			// won't skip this node on the next run
			return err
		}
	} else {
		// For multi-step or vm-delete, check if the last step is vm-delete
		isDelete := false
		if len(m.deps.Config.Action.Steps) > 0 {
			lastStep := m.deps.Config.Action.Steps[len(m.deps.Config.Action.Steps)-1]
			if lastStep.Type == "vm-delete" {
				isDelete = true
			}
		}
		if !isDelete {
			if err := m.uncordonNode(ctx, nodeInfo, step.Type(), days); err != nil {
				return err
			}
		} else {
			log.Printf("node %s: vm-delete completed, old node will be replaced by CMK", nodeInfo.Name)
		}
	}

	// Set action-completed-at ONLY after successful uncordon (or vm-delete)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationActionCompletedAt, time.Now().UTC().Format(time.RFC3339))

	// Only increment for newly remediated nodes
	*globalActive++
	*poolActive++

	return nil
}

func (m *Manager) uncordonNode(ctx context.Context, nodeInfo discovery.NodeInfo, stepType string, days float64) error {
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseUncordoning))
	if err := m.deps.K8sClient.Uncordon(ctx, nodeInfo.Name); err != nil {
		log.Printf("ERROR: failed to uncordon node %s: %v", nodeInfo.Name, err)
		return err
	}
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintMaintenance)
	m.deps.K8sClient.RemoveTaint(ctx, nodeInfo.Name, constants.TaintScheduled)
	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseMonitored))
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationUncordonedAt, time.Now().UTC().Format(time.RFC3339))
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationLastAction, fmt.Sprintf("%s-complete", stepType))
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationAttempt, "0")

	m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeNormal, "NodeRemediated",
		"node remediated with %s after %.1f days uptime", stepType, days)

	log.Printf("node %s remediation complete (step: %s)", nodeInfo.Name, stepType)
	return nil
}

func getAttemptAnnotation(node *corev1.Node) int {
	val := node.Annotations[constants.AnnotationAttempt]
	if val == "" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return n
}

func (m *Manager) countActiveNodes(ctx context.Context) int {
	activePhases := fmt.Sprintf("%s in (%s,%s,%s,%s,%s)",
		constants.LabelRemediationPhase,
		constants.PhaseCordoned, constants.PhaseDraining, constants.PhaseForceDraining,
		constants.PhaseActionRunning, constants.PhaseUncordoning)

	nodes, err := m.deps.K8sClient.ListNodes(ctx, activePhases)
	if err != nil {
		log.Printf("warning: failed to count active nodes: %v", err)
		return 0
	}
	return len(nodes.Items)
}

func (m *Manager) countActiveNodesInPool(ctx context.Context, poolID string) int {
	selector := fmt.Sprintf("%s=%s,%s in (%s,%s,%s,%s,%s)",
		constants.LabelNodepoolID, poolID,
		constants.LabelRemediationPhase,
		constants.PhaseCordoned, constants.PhaseDraining, constants.PhaseForceDraining,
		constants.PhaseActionRunning, constants.PhaseUncordoning)

	nodes, err := m.deps.K8sClient.ListNodes(ctx, selector)
	if err != nil {
		log.Printf("warning: failed to count active nodes in pool %s: %v", poolID, err)
		return 0
	}
	return len(nodes.Items)
}

func (m *Manager) countTotalNodes(ctx context.Context) int {
	nodes, err := m.deps.K8sClient.ListNodes(ctx, "")
	if err != nil {
		log.Printf("warning: failed to count total nodes: %v", err)
		return 0
	}
	return len(nodes.Items)
}

func (m *Manager) totalNodeCount(groups []discovery.NodeGroup) int {
	total := 0
	for _, g := range groups {
		total += len(g.Nodes)
	}
	return total
}
