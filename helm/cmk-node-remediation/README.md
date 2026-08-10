# Node Remediation Helm Chart

Automatically cordons, drains, and takes configurable remediation actions
(vm-reset, vm-stop, vm-start, vm-delete) on B200 GPU nodes approaching the
NVIDIA 60-day NVLink firmware bug threshold.

## Installation

```bash
# Create secret with Crusoe API credentials first
kubectl create secret generic crusoe-api-credentials \
  --namespace cmk-node-remediation \
  --from-literal=api-key=<key> \
  --from-literal=api-url=<url>

# Install with dry-run mode first
helm install cmk-node-remediation ./cmk-node-remediation \
  --namespace cmk-node-remediation \
  --create-namespace \
  --set nodeSelector.matchLabels.crusoe.ai/project.id=<your-project-id> \
  --set crusoeProjectId=<your-project-id> \
  --set dryRun=true
```

## Action Types

| Type | Description |
|------|-------------|
| `vm-reset` | Crusoe API VM RESET + poll node Ready (default) |
| `vm-stop` | Crusoe API VM STOP + poll VM stopped |
| `vm-start` | Crusoe API VM START + poll node Ready |
| `vm-delete` | Crusoe API VM DELETE + wait for new node |
| `noop` | No action (for dry-run / testing) |

## Multi-Step Sequences

```yaml
action:
  maxRetries: 3
  steps:
    - type: vm-stop
      timeout: 5m
    - type: vm-delete
      timeout: 30m
      wait: 2m
```

## Configuration

See values.yaml for all configurable options.
