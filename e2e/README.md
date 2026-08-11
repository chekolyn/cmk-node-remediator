# E2E Tests

End-to-end tests for crusoe-node-remediation.

## Quick Start

```bash
# 1. Copy values template and fill in your cluster details
cp e2e/example-values.yaml e2e/values.yaml
# Edit e2e/values.yaml: set image.repository, nodeSelector, crusoeProjectId

# 2. Run all phases (build + deploy + verify)
bash e2e/run-e2e.sh all --api-key YOUR_CRUSOE_API_KEY

# 3. Or run individual phases
bash e2e/run-e2e.sh build
bash e2e/run-e2e.sh deploy --api-key YOUR_KEY
bash e2e/run-e2e.sh verify
bash e2e/run-e2e.sh dry-run-off
bash e2e/run-e2e.sh vm-reset
bash e2e/run-e2e.sh cleanup
```

## How It Works

The E2E test uses the **same Helm chart** as production — just with a
different `values.yaml`. The workflow is identical:

```bash
# E2E (test cluster, fast thresholds, noop action)
helm install crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  -f e2e/example-values.yaml

# Production (real cluster, 55d/60d thresholds, vm-reset)
helm install crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  -f my-production-values.yaml
```

## Files

| File | Committed? | Purpose |
|------|-----------|---------|
| `e2e/example-values.yaml` | ✅ Yes | Template with test defaults (noop, 30m/1h, dry-run) |
| `e2e/values.yaml` | ❌ No (gitignored) | Your cluster details — copy from template |
| `e2e/run-e2e.sh` | ✅ Yes | Test script — 6 phases, all idempotent |

## Setup

Copy the values template and fill in your cluster details:

```bash
cp e2e/example-values.yaml e2e/values.yaml
```

Edit `e2e/values.yaml`:

```yaml
image:
  repository: registry.your-location.ccr.crusoecloudcompute.com/crusoe-node-remediation.your-project-id
  tag: v0.0.1

nodeSelector:
  matchLabels:
    crusoe.ai/project.id: "your-project-id"

crusoeProjectId: "your-project-id"
```

## Phases

| Phase | Command | What it does | Idempotent? |
|-------|---------|-------------|-------------|
| build | `bash e2e/run-e2e.sh build` | Build + push container image | ✅ |
| deploy | `bash e2e/run-e2e.sh deploy --api-key KEY` | Create secret + install Helm (dry-run) | ✅ |
| verify | `bash e2e/run-e2e.sh verify` | Check logs, events, labels | ✅ (read-only) |
| dry-run-off | `bash e2e/run-e2e.sh dry-run-off` | Upgrade to dryRun=false (noop) | ✅ |
| vm-reset | `bash e2e/run-e2e.sh vm-reset` | Real VM reset (interactive confirm) | ✅ |
| cleanup | `bash e2e/run-e2e.sh cleanup` | Uninstall + uncordon nodes | ✅ |
| all | `bash e2e/run-e2e.sh all --api-key KEY` | build + deploy + verify | ✅ |

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--api-key KEY` | Crusoe API key for K8s secret | (required for deploy) |
| `--values FILE` | Path to values file | `e2e/values.yaml` |
| `--namespace NS` | Kubernetes namespace | `crusoe-node-remediation` |
