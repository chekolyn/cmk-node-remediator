package helpers

import (
	"context"
	"fmt"
	"log"
	"time"

	"cmk-node-remediation/internal/k8s"
)

// sleepOrCancel sleeps for the given duration or returns on context cancellation.
func sleepOrCancel(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// BootVerifier helps steps confirm that a node rebooted.
// Steps capture the bootID before their action, then call VerifyBoot
// to poll until the bootID changes (confirming a boot occurred).
type BootVerifier struct {
	k8sClient *k8s.Client
}

func NewBootVerifier(k8sClient *k8s.Client) *BootVerifier {
	return &BootVerifier{k8sClient: k8sClient}
}

// CaptureBootID returns the current bootID from the node object.
// Call this BEFORE the action (reset, start, delete).
func (v *BootVerifier) CaptureBootID(ctx context.Context, nodeName string) (string, error) {
	node, err := v.k8sClient.GetNode(ctx, nodeName)
	if err != nil {
		return "", err
	}
	return node.Status.NodeInfo.BootID, nil
}

// VerifyBoot polls until the node's bootID differs from the given one,
// confirming a boot occurred. Returns the new bootID.
func (v *BootVerifier) VerifyBoot(ctx context.Context, nodeName, oldBootID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	pollInterval := 10 * time.Second

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		node, err := v.k8sClient.GetNode(ctx, nodeName)
		if err != nil {
			// Node might be temporarily unreachable during reboot
			log.Printf("boot verifier: failed to get node %s: %v", nodeName, err)
			sleepOrCancel(ctx, pollInterval)
			continue
		}

		newBootID := node.Status.NodeInfo.BootID
		if newBootID != "" && newBootID != oldBootID {
			return newBootID, nil
		}

		sleepOrCancel(ctx, pollInterval)
	}

	return "", fmt.Errorf("node %s bootID did not change within %v", nodeName, timeout)
}

// VerifyBootAndReady polls until bootID changes AND node is Ready.
// Convenience method for steps that need both.
func (v *BootVerifier) VerifyBootAndReady(ctx context.Context, nodeName, oldBootID string, timeout time.Duration) error {
	// First: wait for bootID to change (boot happened)
	if _, err := v.VerifyBoot(ctx, nodeName, oldBootID, timeout); err != nil {
		return err
	}

	// Second: wait for node Ready (boot completed)
	deadline := time.Now().Add(timeout)
	pollInterval := 30 * time.Second
	// Use shorter poll interval if timeout is short (for testing)
	if timeout < pollInterval {
		pollInterval = timeout / 3
		if pollInterval < 100*time.Millisecond {
			pollInterval = 100 * time.Millisecond
		}
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		node, err := v.k8sClient.GetNode(ctx, nodeName)
		if err != nil {
			sleepOrCancel(ctx, pollInterval)
			continue
		}

		if k8s.IsNodeReady(node) {
			return nil
		}

		sleepOrCancel(ctx, pollInterval)
	}

	return fmt.Errorf("node %s did not become Ready within %v", nodeName, timeout)
}
