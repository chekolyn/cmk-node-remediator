# E2E Tests

End-to-end tests for cmk-node-remediation.

## Quick Start

```bash
# 1. Copy config and set your API key
cp e2e/config.env e2e/local.env
# Edit local.env: set CRUSOE_API_KEY

# 2. Run all phases (build + deploy + verify)
bash e2e/run-e2e.sh all

# 3. Or run individual phases
bash e2e/run-e2e.sh build
bash e2e/run-e2e.sh deploy --api-key YOUR_KEY
bash e2e/run-e2e.sh verify
bash e2e/run-e2e.sh dry-run-off
bash e2e/run-e2e.sh vm-reset
bash e2e/run-e2e.sh cleanup
```

## Configuration

All settings are parameterized via environment variables with defaults
for the `cmk-test-cluster` test cluster.

Copy `e2e/config.env` to `e2e/local.env` and customize:

```bash
# Cluster
CLUSTER_NAME=cmk-test-cluster
CLUSTER_ID=your-cluster-id
PROJECT_ID=your-project-id
LOCATION=us-southcentral1-a

# Registry
REGISTRY_URL=registry.us-southcentral1-a.ccr.crusoecloudcompute.com
REGISTRY_REPO=crusoe-node-remediation.your-project-id
IMAGE_TAG=v0.0.1

# Crusoe API
CRUSOE_API_URL=https://api.cloud.crusoe.ai/v1alpha5
CRUSOE_API_KEY=  # SET THIS

# Test thresholds (fast for testing)
VM_INSTANCE_TYPE_FILTER=c1a.*
CORDON_THRESHOLD=30m
ACTION_THRESHOLD=1h
COOLDOWN=15m
SCHEDULE=*/5 * * * *
```

Or override via env vars or command line:

```bash
# Different cluster
CLUSTER_NAME=example-gpu-cluster PROJECT_ID=your-project-id... bash e2e/run-e2e.sh deploy

# Different VM type filter
VM_INSTANCE_TYPE_FILTER=b200.* bash e2e/run-e2e.sh deploy

# Pass API key via flag
bash e2e/run-e2e.sh deploy --api-key YOUR_KEY
```

## Phases

| Phase | Command | What it does | Idempotent? |
|-------|---------|-------------|-------------|
| build | `bash e2e/run-e2e.sh build` | Build + push container image | ✅ Yes |
| deploy | `bash e2e/run-e2e.sh deploy` | Create secret + install Helm (dry-run) | ✅ Yes |
| verify | `bash e2e/run-e2e.sh verify` | Check logs, events, labels | ✅ Yes (read-only) |
| dry-run-off | `bash e2e/run-e2e.sh dry-run-off` | Upgrade to dryRun=false (noop) | ✅ Yes |
| vm-reset | `bash e2e/run-e2e.sh vm-reset` | Real VM reset (interactive confirm) | ✅ Yes |
| cleanup | `bash e2e/run-e2e.sh cleanup` | Uninstall + uncordon nodes | ✅ Yes |
| all | `bash e2e/run-e2e.sh all` | build + deploy + verify | ✅ Yes |

## Idempotency

- **Secret**: `kubectl create secret --dry-run=client -o yaml | kubectl apply -f -` — creates or updates
- **Helm**: `helm upgrade --install` — installs or upgrades
- **Cleanup**: uncordons nodes, removes labels/taints, deletes namespace
- **Values**: generated from env vars, no hardcoded files to manage

## Using with a Different Cluster

```bash
# Example: a large customer cluster
CLUSTER_NAME=example-gpu-cluster \
CLUSTER_ID=your-cluster-id \
PROJECT_ID=your-project-id \
LOCATION=eu-norway1-a \
VM_INSTANCE_TYPE_FILTER=b200.* \
CORDON_THRESHOLD=55d \
ACTION_THRESHOLD=60d \
COOLDOWN=7d \
SCHEDULE="0 0 * * *" \
GLOBAL_MAX_CORDONED=5 \
PER_POOL_MAX_CORDONED=2 \
bash e2e/run-e2e.sh deploy --api-key YOUR_KEY
```
