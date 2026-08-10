package actions

import (
	"context"
	"fmt"
	"log"
	"time"

	"crusoe-cmk-node-remediation/internal/actions/helpers"
	"crusoe-cmk-node-remediation/internal/crusoe"
	"crusoe-cmk-node-remediation/internal/k8s"
)

// VMResetStep reboots a VM via the Crusoe API and waits for the node to become Ready.
// Idempotent: if the node is already Ready, it skips the API call.
// Two-phase verification: (1) poll Crusoe operation completion, (2) poll K8s bootID + Ready.
type VMResetStep struct {
	crusoeClient  *crusoe.Client
	k8sClient    *k8s.Client
	bootVerifier  *helpers.BootVerifier
	timeout       time.Duration
	maxRetries    int
}

func (s *VMResetStep) Type() string { return "vm-reset" }

func (s *VMResetStep) Run(ctx context.Context, node NodeInfo, params map[string]string) error {
	// 1. Idempotency check: is the node already Ready?
	k8sNode, err := s.k8sClient.GetNode(ctx, node.Name)
	if err == nil && k8s.IsNodeReady(k8sNode) {
		log.Printf("step vm-reset: node %s already Ready, skipping", node.Name)
		return nil
	}

	// 2. Capture bootID before reset
	oldBootID := ""
	if k8sNode != nil {
		oldBootID = k8sNode.Status.NodeInfo.BootID
	}

	// 3. Act + verify with retries
	var lastErr error
	for attempt := 1; attempt <= s.maxRetries; attempt++ {
		log.Printf("step vm-reset: attempt %d/%d for node %s", attempt, s.maxRetries, node.Name)

		// Phase 1: Call Crusoe API RESET
		opID, err := s.crusoeClient.RebootVM(ctx, node.InstanceID)
		if err != nil {
			lastErr = err
			log.Printf("step vm-reset: API call failed (attempt %d): %v", attempt, err)
			sleepOrCancel(ctx, time.Duration(1<<uint(attempt-1))*time.Second)
			continue
		}

		// Phase 1: Poll operation completion
		if opID != "" {
			if err := s.crusoeClient.WaitForOperation(ctx, opID, s.timeout); err != nil {
				lastErr = err
				log.Printf("step vm-reset: operation wait failed (attempt %d): %v", attempt, err)
				continue
			}
		}

		// Phase 2: Verify boot happened + node is Ready
		if err := s.bootVerifier.VerifyBootAndReady(ctx, node.Name, oldBootID, s.timeout); err != nil {
			lastErr = err
			log.Printf("step vm-reset: boot verification failed (attempt %d): %v", attempt, err)
			continue
		}

		return nil
	}

	return fmt.Errorf("step vm-reset failed after %d attempts: %w", s.maxRetries, lastErr)
}
