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

// DrainManager interface for node draining.
type DrainManager interface {
	Drain(ctx context.Context, nodeName string) error
	ForceDrain(ctx context.Context, nodeName string) error
}

// StepCreator interface for creating remediation steps.
type StepCreator interface {
	CreateFromConfig(cfg config.ActionConfig) (actions.Step, error)
}

// Dependencies holds all the components the manager needs.
type Dependencies struct {
	K8sClient    *k8s.Client
	DrainMgr     DrainManager
	StepFactory  StepCreator
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

		// Query uptime for all nodes in this pool before processing
		type nodeWithUptime struct {
			info   discovery.NodeInfo
			uptime time.Duration
		}
		var nodesWithUptime []nodeWithUptime

		for _, nodeInfo := range group.Nodes {
			startTime, err := m.deps.UptimeEval.GetNodeStartTime(ctx, nodeInfo.Name)
			if err != nil {
				log.Printf("skipping node %s: failed to get uptime: %v", nodeInfo.Name, err)
				continue
			}

			uptime := time.Since(startTime)

			// Check cooldown — skip if remediated recently
			if m.isInCooldown(nodeInfo.Node) {
				log.Printf("skipping node %s: in cooldown (remediated within %v)", nodeInfo.Name, time.Duration(m.deps.Config.Cooldown))
				continue
			}

			nodesWithUptime = append(nodesWithUptime, nodeWithUptime{info: nodeInfo, uptime: uptime})
		}

		// Sort nodes by priority
		prioritizer, err := GetPrioritizer(m.deps.Config.NodePriority)
		if err != nil {
			log.Printf("warning: %v, using default (highest-uptime)", err)
			prioritizer = highestUptime{}
		}

		// Convert to NodeWithUptime for sorting
		nodesForSort := make([]NodeWithUptime, len(nodesWithUptime))
		for i, nwu := range nodesWithUptime {
			nodesForSort[i] = NodeWithUptime{
				Name:       nwu.info.Name,
				InstanceID: nwu.info.InstanceID,
				Uptime:     nwu.uptime,
			}
		}
		prioritizer.Sort(nodesForSort)

		if m.deps.Config.NodePriority != "" && m.deps.Config.NodePriority != "none" {
			log.Printf("pool %s: processing %d nodes by %s priority", group.NodepoolID, len(nodesForSort), prioritizer.Name())
		}

		// Build a lookup from sorted names back to original nodeWithUptime
		lookup := make(map[string]nodeWithUptime, len(nodesWithUptime))
		for _, nwu := range nodesWithUptime {
			lookup[nwu.info.Name] = nwu
		}

		for _, nfs := range nodesForSort {
			nwu := lookup[nfs.Name]
			action := ClassifyUptime(nwu.uptime, time.Duration(m.deps.Config.CordonThreshold), time.Duration(m.deps.Config.ActionThreshold))

			switch action {
			case ActionNone:
				m.handleMonitoredNode(ctx, nwu.info)
			case ActionCordon:
				m.handleCordonNode(ctx, nwu.info, nwu.uptime, &globalActive, &poolActive, globalTotal, poolTotal)
			case ActionRemediate:
				if err := m.handleRemediateNode(ctx, nwu.info, nwu.uptime, &globalActive, &poolActive, globalTotal, poolTotal); err != nil {
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
	cooldownDuration := time.Duration(m.deps.Config.Cooldown)
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
			log.Printf("[dry-run] would uncordon node %s (uptime < %v)",
			nodeInfo.Name, time.Duration(m.deps.Config.CordonThreshold))
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

func (m *Manager) handleCordonNode(ctx context.Context, nodeInfo discovery.NodeInfo, uptime time.Duration, globalActive, poolActive *int, globalTotal, poolTotal int) {
	if !m.deps.GuardChecker.CanCordon(*globalActive, *poolActive, globalTotal, poolTotal) {
		log.Printf("skipping node %s: guardrail exceeded (global=%d/%d, pool=%d/%d)",
			nodeInfo.Name, *globalActive, globalTotal, *poolActive, poolTotal)
		return
	}

	if m.deps.Config.DryRun {
		log.Printf("[dry-run] would cordon node %s (uptime %v)", nodeInfo.Name, uptime)
		return
	}

	log.Printf("cordoning node %s (uptime %v)", nodeInfo.Name, uptime)

	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationManaged, "true")

	if err := m.deps.K8sClient.Cordon(ctx, nodeInfo.Name); err != nil {
		log.Printf("ERROR: failed to cordon node %s: %v", nodeInfo.Name, err)
		return
	}

	m.deps.K8sClient.SetLabel(ctx, nodeInfo.Name, constants.LabelRemediationPhase, string(constants.PhaseCordoned))
	m.deps.K8sClient.AddTaint(ctx, nodeInfo.Name, constants.TaintScheduled)

	now := time.Now().UTC().Format(time.RFC3339)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationCordonedAt, now)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationCordonUptime, fmt.Sprintf("%v", uptime))
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationManagedBy, constants.ManagedByValue)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationLastAction, "cordon")
	m.deps.Recorder.Eventf(nodeInfo.Node, corev1.EventTypeNormal, "NodeCordoned", "node cordoned at %v uptime", uptime)

	// Only increment for newly cordoned nodes
	*globalActive++
	*poolActive++
}

func (m *Manager) handleRemediateNode(ctx context.Context, nodeInfo discovery.NodeInfo, uptime time.Duration, globalActive, poolActive *int, globalTotal, poolTotal int) error {
	if !m.deps.GuardChecker.CanCordon(*globalActive, *poolActive, globalTotal, poolTotal) {
		log.Printf("skipping node %s: guardrail exceeded (global=%d/%d, pool=%d/%d)",
			nodeInfo.Name, *globalActive, globalTotal, *poolActive, poolTotal)
		return nil
	}

	if m.deps.Config.DryRun {
		log.Printf("[dry-run] would remediate node %s with action %s (uptime %v)",
			nodeInfo.Name, m.deps.Config.Action.Type, uptime)
		return nil
	}

log.Printf("remediating node %s with action %s (uptime %v)",
		nodeInfo.Name, m.deps.Config.Action.Type, uptime)

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
	// Determine if the last action is vm-delete (single or multi-step)
	isDeleteAction := false
	if len(m.deps.Config.Action.Steps) > 0 {
		lastStep := m.deps.Config.Action.Steps[len(m.deps.Config.Action.Steps)-1]
		isDeleteAction = lastStep.Type == "vm-delete"
	} else if m.deps.Config.Action.Type == "vm-delete" {
		isDeleteAction = true
	}

	if isDeleteAction {
		log.Printf("node %s: vm-delete completed, old node will be replaced by CMK", nodeInfo.Name)
	} else {
		if err := m.uncordonNode(ctx, nodeInfo, step.Type(), uptime); err != nil {
			// Uncordon failed — don't set action-completed-at, so cooldown
			// won't skip this node on the next run
			return err
		}
	}

	// Set action-completed-at ONLY after successful uncordon (or vm-delete)
	m.deps.K8sClient.SetAnnotation(ctx, nodeInfo.Name, constants.AnnotationActionCompletedAt, time.Now().UTC().Format(time.RFC3339))

	// Only increment for newly remediated nodes
	*globalActive++
	*poolActive++

	return nil
}

func (m *Manager) uncordonNode(ctx context.Context, nodeInfo discovery.NodeInfo, stepType string, uptime time.Duration) error {
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
		"node remediated with %s after %v uptime", stepType, uptime)

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
