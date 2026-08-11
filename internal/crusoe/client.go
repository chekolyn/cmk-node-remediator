package crusoe

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/antihax/optional"
	crusoe "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

// vmUptimeInfo is a lightweight struct for testing uptime calculation.
type vmUptimeInfo struct {
	ID        string
	CreatedAt string
}

// Client wraps the crusoecloud/client-go SDK for VM operations.
type Client struct {
	vmsAPI         *crusoe.VMsApiService
	vmOpsAPI       *crusoe.VMOperationsApiService
	projectID      string
}

// NewClient creates a new Crusoe API client using the official SDK.
func NewClient(apiURL, apiKey, projectID string) *Client {
	cfg := crusoe.NewConfiguration()
	cfg.BasePath = apiURL
	cfg.AddDefaultHeader("Authorization", "Bearer "+apiKey)

	apiClient := crusoe.NewAPIClient(cfg)

	return &Client{
		vmsAPI:    apiClient.VMsApi,
		vmOpsAPI:  apiClient.VMOperationsApi,
		projectID: projectID,
	}
}

// GetVMState queries the Crusoe API for a VM's current state.
func (c *Client) GetVMState(ctx context.Context, instanceID string) (string, error) {
	vm, _, err := c.vmsAPI.GetInstance(ctx, c.projectID, instanceID)
	if err != nil {
		return "", fmt.Errorf("Crusoe API GetInstance failed for VM %s: %w", instanceID, err)
	}
	return vm.State, nil
}

// RebootVM triggers a VM reset via the Crusoe API (Action: RESET).
// Returns the operation ID for async polling.
func (c *Client) RebootVM(ctx context.Context, instanceID string) (string, error) {
	return c.updateVMAction(ctx, instanceID, "RESET")
}

// StopVM stops a VM via the Crusoe API (Action: STOP).
// Returns the operation ID for async polling.
func (c *Client) StopVM(ctx context.Context, instanceID string) (string, error) {
	return c.updateVMAction(ctx, instanceID, "STOP")
}

// StartVM starts a VM via the Crusoe API (Action: START).
// Returns the operation ID for async polling.
func (c *Client) StartVM(ctx context.Context, instanceID string) (string, error) {
	return c.updateVMAction(ctx, instanceID, "START")
}

// DeleteVM deletes a VM via the Crusoe API.
// Returns the operation ID for async polling.
func (c *Client) DeleteVM(ctx context.Context, instanceID string) (string, error) {
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, _, err := c.vmsAPI.DeleteInstance(ctx, c.projectID, instanceID)
		if err == nil {
			if resp.Operation != nil {
				return resp.Operation.OperationId, nil
			}
			return "", nil
		}

		lastErr = err
		errStr := err.Error()
		if strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "500") ||
			strings.Contains(errStr, "502") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "504") {
			continue
		}

		return "", fmt.Errorf("Crusoe API delete failed for VM %s: %w", instanceID, err)
	}

	return "", fmt.Errorf("DeleteVM failed after %d retries: %w", maxRetries, lastErr)
}

// WaitForOperation polls the VMOperationsApi until the async operation completes.
func (c *Client) WaitForOperation(ctx context.Context, operationID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pollInterval := 10 * time.Second

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		op, _, err := c.vmOpsAPI.GetComputeVMsInstancesOperation(ctx, c.projectID, operationID)
		if err != nil {
			log.Printf("warning: failed to poll operation %s: %v", operationID, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval):
			}
			continue
		}

		if op.CompletedAt != "" {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("operation %s did not complete within %v", operationID, timeout)
}

// updateVMAction is a helper for UpdateInstance with retry logic.
// Returns the operation ID for async polling.
func (c *Client) updateVMAction(ctx context.Context, instanceID, action string) (string, error) {
	patchReq := crusoe.InstancesPatchRequestV1Alpha5{
		Action: action,
	}

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, _, err := c.vmsAPI.UpdateInstance(ctx, patchReq, c.projectID, instanceID)
		if err == nil {
			if resp.Operation != nil {
				return resp.Operation.OperationId, nil
			}
			return "", nil
		}

		lastErr = err
		errStr := err.Error()
		if strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "500") ||
			strings.Contains(errStr, "502") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "504") {
			continue
		}

		return "", fmt.Errorf("Crusoe API %s failed for VM %s: %w", action, instanceID, err)
	}

	return "", fmt.Errorf("%s failed after %d retries: %w", action, maxRetries, lastErr)
}

// calculateUptimes is a pure function for testing uptime calculation.
func calculateUptimes(vms []vmUptimeInfo, now time.Time) map[string]float64 {
	uptimes := make(map[string]float64)
	for _, vm := range vms {
		if vm.CreatedAt == "" {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339, vm.CreatedAt)
		if err != nil {
			continue
		}
		uptime := now.Sub(createdAt).Hours() / 24.0
		uptimes[vm.ID] = uptime
	}
	return uptimes
}

// listInstances is a helper for testing that uses the SDK's ListInstances.
func (c *Client) listInstances(ctx context.Context) ([]crusoe.InstanceV1Alpha5, error) {
	var allVMs []crusoe.InstanceV1Alpha5
	nextToken := ""
	for {
		opts := &crusoe.VMsApiListInstancesOpts{
			Limit:     optional.NewString("500"),
			NextToken: optional.NewString(nextToken),
		}

		resp, _, err := c.vmsAPI.ListInstances(ctx, c.projectID, opts)
		if err != nil {
			return nil, fmt.Errorf("Crusoe API ListInstances failed: %w", err)
		}

		allVMs = append(allVMs, resp.Items...)

		if resp.NextPageToken == "" {
			break
		}
		nextToken = resp.NextPageToken
	}

	return allVMs, nil
}
