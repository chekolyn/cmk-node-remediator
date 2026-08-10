# E2E Tests

End-to-end tests for cmk-node-remediation on the test CMK cluster
`cmk-test-cluster`.

## Prerequisites

- Docker running
- `kubectl` configured for `cmk-test-cluster`
- `crusoe` CLI configured
- `helm` installed

## Test Phases

### Phase 1: Build and push container image
```bash
bash e2e/run-e2e.sh build
```

### Phase 2: Deploy in dry-run mode
```bash
bash e2e/run-e2e.sh deploy
```
Installs the Helm chart with `dryRun=true` and `noop` action. No changes to nodes.

### Phase 3: Verify node discovery
```bash
bash e2e/run-e2e.sh verify
```
Checks logs for correct node targeting, events, and labels.

### Phase 4: Dry-run off (noop action)
```bash
bash e2e/run-e2e.sh dry-run-off
```
Upgrades to `dryRun=false` with `noop` action. Will cordon/drain/uncordon nodes
but won't call Crusoe API.

### Phase 5: Real VM reset
```bash
bash e2e/run-e2e.sh vm-reset
```
Upgrades to `vm-reset` action. Will actually call Crusoe API to reset VMs.
**Only run after Phase 4 is verified.**

### Phase 6: Cleanup
```bash
bash e2e/run-e2e.sh cleanup
```
Uninstalls Helm chart and uncordons any managed nodes.

### All phases (build + deploy + verify)
```bash
bash e2e/run-e2e.sh all
```

## Values Files

| File | Description |
|------|-------------|
| `values-test-cluster.yaml` | Dry-run mode, noop action, every 5 min |
| `values-test-cluster-dry-run-off.yaml` | dryRun=false, noop action, every 5 min |
| `values-test-cluster-vm-reset.yaml` | dryRun=false, vm-reset action, every 10 min |

## Test Cluster Details

- **Cluster:** `cmk-test-cluster`
- **Project ID:** `your-project-id`
- **Nodepool:** `your-nodepool-id`
- **VM type:** `c1a.2x` (CPU nodes, not B200)
- **Nodes:** 5

## Key differences from production

| Setting | Test | Production |
|---------|------|------------|
| `vmInstanceTypeFilter` | `c1a.*` | `b200.*` |
| `cordonDays` | 1 | 55 |
| `actionDays` | 2 | 60 |
| `cooldownDays` | 1 | 7 |
| `schedule` | `*/5 * * * *` | `0 0 * * *` |
| `action.type` | `noop` → `vm-reset` | `vm-reset` |
| `globalMaxCordoned` | 2 | 5 |
| `perPoolMaxCordoned` | 1 | 2 |
