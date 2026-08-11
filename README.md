# Crusoe Node Remediation

A Kubernetes CronJob that automatically cordons, drains, and remediates nodes
approaching critical uptime thresholds. Built for Crusoe Managed Kubernetes (CMK)
clusters with B200 GPU nodes affected by the NVIDIA 60-day NVLink firmware bug.

---

## TLDR

```bash
# 1. Create API secret
kubectl create secret generic crusoe-api-credentials \
  --namespace crusoe-node-remediation \
  --from-literal=api-key=YOUR_API_KEY \
  --from-literal=api-url=https://api.cloud.crusoe.ai/v1alpha5

# 2. Install with Helm (observation mode — no changes to nodes)
helm install crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  --create-namespace \
  --set dryRun=true \
  --set crusoeProjectId=YOUR_PROJECT_ID \
  --set nodeSelector.matchLabels.crusoe\.ai/project\.id=YOUR_PROJECT_ID

# 3. Watch the logs
kubectl logs -n crusoe-node-remediation -f job/$(kubectl get jobs -n crusoe-node-remediation --sort-by=.metadata.creationTimestamp -o name | tail -1)

# 4. When ready, switch to live mode
helm upgrade crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  --set dryRun=false
```

---

## What It Does

This tool runs as a CronJob and performs the following on each scheduled run:

1. **Discovers** nodes matching your label selector and instance type filter
2. **Queries uptime** via the K8s stats summary API (`node.startTime`)
3. **Cordons** nodes that exceed `cordonThreshold` uptime
4. **Drains** nodes that exceed `remediationThreshold` uptime (graceful → force)
5. **Executes** the configured remediation action (`vm-reset`, `noop`, etc.)
6. **Uncordons** nodes after successful remediation
7. **Skips** nodes in cooldown (recently remediated) or running this pod

### Safety Features

- **Guardrails** — limits how many nodes can be cordoned simultaneously (absolute or percentage)
- **Self-node exclusion** — never drains the node running the CronJob pod
- **Cooldown** — skips nodes remediated within the cooldown period
- **Max retries** — leaves nodes cordoned for manual intervention after N failed attempts
- **Dry-run mode** — logs all actions without executing cordon/drain/remediation
- **Required affinity** — pod won't schedule on nodes being remediated
- **Tolerations** — pod can schedule on cordoned nodes (safety net for when all nodes are cordoned)

---

## Getting Started

### Prerequisites

- Crusoe Managed Kubernetes (CMK) cluster
- Crusoe API key (for VM actions like `vm-reset`)
- Crusoe Container Registry access (for pulling the image)
- Helm 3.x
- `kubectl` with cluster admin access

### Step 1: Create Secrets

```bash
# Create namespace
kubectl create namespace crusoe-node-remediation

# Crusoe API credentials (for VM actions)
kubectl create secret generic crusoe-api-credentials \
  --namespace crusoe-node-remediation \
  --from-literal=api-key=YOUR_API_KEY \
  --from-literal=api-url=https://api.cloud.crusoe.ai/v1alpha5

# Image pull secret (for Crusoe Container Registry)
# Username: your Crusoe account email
# Password: full token string from `crusoe registry tokens create`
kubectl create secret docker-registry crusoe-registry-pull \
  --namespace crusoe-node-remediation \
  --docker-server=registry.YOUR-LOCATION.ccr.crusoecloudcompute.com \
  --docker-username=YOUR_EMAIL@crusoe.ai \
  --docker-password=YOUR_REGISTRY_TOKEN
```

### Step 2: Create a values.yaml

```yaml
image:
  repository: registry.YOUR-LOCATION.ccr.crusoecloudcompute.com/crusoe-node-remediation.YOUR-PROJECT-ID/crusoe-node-remediation
  tag: v0.0.11
  pullPolicy: IfNotPresent

imagePullSecret: crusoe-registry-pull

# Target nodes in your project
nodeSelector:
  matchLabels:
    crusoe.ai/project.id: "YOUR-PROJECT-ID"

instanceTypeFilter: "b200.*"

# Start conservative — only 1 node at a time
guardrails:
  globalMaxCordoned: 1
  perPoolMaxCordoned: 1

# B200 60-day NVLink bug thresholds
cordonThreshold: 55d
remediationThreshold: 60d
remediationCooldown: 7d

action:
  type: vm-reset
  timeout: 30m
  maxRetries: 3

drainTimeout: 10m
forceAfterEvictionFailure: true

crusoeApiSecret: crusoe-api-credentials
crusoeProjectId: "YOUR-PROJECT-ID"

# Start in observation mode
dryRun: true

schedule: "0 * * * *"   # every hour
```

### Step 3: Deploy in Observation Mode

```bash
helm install crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  -f values.yaml
```

Check the logs:
```bash
kubectl logs -n crusoe-node-remediation -f job/$(kubectl get jobs -n crusoe-node-remediation --sort-by=.metadata.creationTimestamp -o name | tail -1)
```

You'll see output like:
```
config: thresholds=(cordon=55d, remediation=60d, cooldown=7d), action=(type=vm-reset, dryRun=true), guardrails=(global=1, perPool=1), drain=(timeout=10m, force=true)
cluster: 458 nodes (450 ready, 3 notReady, 0 remediating)
discovered 12 nodepools, 200 targeted nodes
targeting: crusoe.ai/project.id=YOUR-PROJECT-ID, beta.kubernetes.io/instance-type=~b200.*
guardrails: 0 remediating / 1 max
[dry-run] would remediate node node-gpu-42 with action vm-reset (uptime 60d12h)
[dry-run] would remediate node node-gpu-17 with action vm-reset (uptime 60d5h)
```

### Step 4: Progressively Enable

Follow this safe progression:

| Phase | Config | What happens |
|-------|--------|-------------|
| **1. Observe** | `dryRun: true` | Logs what *would* happen — no changes to nodes |
| **2. Cordon only** | `dryRun: false`, `action: noop` | Cordons + drains + uncordons — no VM actions |
| **3. Remediate** | `dryRun: false`, `action: vm-reset` | Full cycle: cordon → drain → VM reset → uncordon |

#### Phase 1: Observe (days 1-2)

Keep `dryRun: true`. Verify:
- Correct nodes are discovered
- Uptime values look correct
- Guardrail limits are appropriate
- No unexpected nodes targeted

#### Phase 2: Cordon + Drain (days 3-5)

```bash
helm upgrade crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  -f values.yaml \
  --set dryRun=false \
  --set action.type=noop
```

This does everything except the VM action:
- Cordons the node (marks unschedulable)
- Drains pods (graceful → force if needed)
- Runs noop (does nothing)
- Uncordons the node (back to schedulable)

Verify:
- Drain succeeds (check `kubectl get events -n default`)
- Pods reschedule correctly
- Nodes come back to Ready after uncordon
- Guardrails prevent too many nodes being drained at once

#### Phase 3: Full Remediation (day 6+)

```bash
helm upgrade crusoe-node-remediation ./helm/crusoe-node-remediation \
  --namespace crusoe-node-remediation \
  -f values.yaml \
  --set dryRun=false \
  --set action.type=vm-reset
```

This does the full cycle:
- Cordon → Drain → VM reset via Crusoe API → Wait for node Ready → Uncordon

Verify:
- VM reset succeeds (check `kubectl get events -n default` for `NodeRemediated`)
- `bootID` changes after reset (confirms reboot)
- Node comes back Ready
- Uptime resets to near-zero after reboot

---

## Configuration

See [docs/configuration.md](docs/configuration.md) for the full reference.

### Key Settings

| Setting | Default | Description |
|--------|---------|-------------|
| `dryRun` | `false` | Log only, don't touch nodes |
| `cordonThreshold` | `55d` | Uptime at which to cordon |
| `remediationThreshold` | `60d` | Uptime at which to remediate |
| `remediationCooldown` | `7d` | Skip nodes remediated within this period |
| `action.type` | `vm-reset` | Remediation action (`vm-reset`, `vm-stop`, `vm-start`, `vm-delete`, `noop`) |
| `action.maxRetries` | `3` | Max attempts before leaving cordoned for manual intervention |
| `guardrails.globalMaxCordoned` | `5` | Max nodes being remediated cluster-wide |
| `guardrails.perPoolMaxCordoned` | `2` | Max nodes being remediated per nodepool |
| `drainTimeout` | `10m` | Graceful drain timeout |
| `forceAfterEvictionFailure` | `true` | Bypass PDBs and delete emptyDir data if [eviction](https://kubernetes.io/docs/concepts/scheduling-eviction/api-eviction/) fails |
| `schedule` | `0 * * * *` | Cron schedule (every hour) |
| `instanceTypeFilter` | `b200.*` | Regex filter for `beta.kubernetes.io/instance-type` label |

### Conservative Starting Point

For production with minimal risk, start with:

```yaml
guardrails:
  globalMaxCordoned: 1        # only 1 node at a time
  perPoolMaxCordoned: 1

drainTimeout: 15m             # be patient with drain
forceAfterEvictionFailure: false             # don't force — leave cordoned if drain fails

action:
  type: vm-reset
  maxRetries: 2               # fewer retries = faster manual intervention

remediationCooldown: 14d      # long cooldown — don't re-remediate quickly
```

### Tuning the Schedule

The CronJob schedule determines how frequently the tool scans for nodes needing remediation.
Choose a cadence that allows the **longest drain** in your cluster to complete **within a single run**
— otherwise overlapping runs are blocked by `concurrencyPolicy: Forbid` and nodes may pile up.

**Key factors:**

| Factor | How it affects schedule |
|--------|----------------------|
| `drainTimeout` | The tool waits up to this long for graceful drain. Schedule must be ≥ `drainTimeout` + action timeout + buffer. |
| Pod graceful termination | Long `terminationGracePeriodSeconds` on workloads extends drain time. |
| PDB quorum | If PDBs block eviction, drain stalls until `drainTimeout` — then force drain kicks in. |
| `action.timeout` | VM reset/stop/start polling adds to total run time. |
| Number of nodes | With `globalMaxCordoned: 1`, each node is processed sequentially. More nodes = longer total run. |

**Suggested cadences:**

| Workload profile | Schedule | Rationale |
|------------------|----------|----------|
| Fast-draining (stateless, low PDB impact) | `0 * * * *` (every hour) | Drain completes in minutes; hourly is responsive enough |
| Moderate (mixed stateless/stateful, PDBs) | `0 */2 * * *` (every 2h) | Allows 10–15m drain + 30m action + buffer within one run |
| Slow-draining (large stateful, long grace periods) | `0 */4 * * *` (every 4h) | Accommodates 30m+ drain, long VM reset polling, and cooldown between nodes |
| Conservative / low-urgency | `0 */6 * * *` (every 6h) | For 60-day thresholds — 6h granularity is plenty; minimizes API noise |

**Rule of thumb:**

> `schedule interval ≥ drainTimeout + action.timeout + (globalMaxCordoned × per-node overhead)`
>
> Where per-node overhead ≈ 2–5 min for cordon/uncordon/label updates.

**Example:** If `drainTimeout: 15m`, `action.timeout: 30m`, `globalMaxCordoned: 1`:
>
> Minimum interval ≈ 15m + 30m + 5m = 50m → round up to **every hour** (`0 * * * *`).

If you have many nodes to process in a single run (e.g., 5+ at `globalMaxCordoned: 5`),
multiply the per-node cost and widen the interval accordingly.

### Tuning Guardrails

Guardrails can be absolute count, percentage, or both (more restrictive wins):

```yaml
guardrails:
  globalMaxCordoned: 5            # max 5 nodes cluster-wide
  globalMaxCordonedPercent: 10    # OR 10% of cluster (whichever is smaller)
  perPoolMaxCordoned: 2           # max 2 nodes per nodepool
  perPoolMaxCordonedPercent: 25   # OR 25% of pool (whichever is smaller)
```

The logs show the calculation:
```
guardrails: 2 remediating / 3 max
guardrail config: globalMax=5, globalMaxPercent=10% (→3 of 30), perPoolMax=2
```

---

## Monitoring

### Logs

```bash
# Latest job logs
kubectl logs -n crusoe-node-remediation -f job/$(kubectl get jobs -n crusoe-node-remediation --sort-by=.metadata.creationTimestamp -o name | tail -1)
```

### K8s Events

Node events are created in the `default` namespace:

```bash
kubectl get events -n default --sort-by='.lastTimestamp' | grep -E "NodeCordoned|NodeRemediated|DrainBlocked|ForceDrainFailed|MaxRetriesExceeded"
```

| Event | Type | Description |
|-------|------|-------------|
| `NodeCordoned` | Normal | Node marked unschedulable |
| `DrainBlocked` | Warning | Graceful drain failed (PDB, local storage) |
| `ForceDrainFailed` | Warning | Force drain also failed |
| `NodeRemediated` | Normal | Full cycle completed successfully |
| `NodeUncordoned` | Normal | Node back to schedulable |
| `MaxRetriesExceeded` | Warning | Node left cordoned — manual intervention needed |

### Node Labels & Annotations

```bash
# Check remediation state
kubectl get nodes -o custom-columns='NAME:.metadata.name,PHASE:.metadata.labels.crusoe\.ai/remediation\.phase,CORDONED:.spec.unschedulable'

# Check annotations
kubectl get node NODE_NAME -o jsonpath='{.metadata.annotations}' | jq | grep crusoe
```

| Label/Annotation | Description |
|------------------|-------------|
| `crusoe.ai/remediation.managed=true` | Node is managed by this tool |
| `crusoe.ai/remediation.phase` | Current phase: `cordoned`, `draining`, `force-draining`, `action-running`, `uncordoning`, `monitored` |
| `crusoe.ai/remediation.action-completed-at` | Timestamp of last successful remediation (used for cooldown) |
| `crusoe.ai/remediation.attempt` | Current retry count (reset to 0 on success) |
| `crusoe.ai/remediation.cordoned-at` | When the node was cordoned |
| `crusoe.ai/remediation.drain-started-at` | When drain started |
| `crusoe.ai/remediation.last-action` | Last action taken (`cordon`, `noop-complete`, `vm-reset-complete`, etc.) |

---

## Remediation Actions

| Action | Description | Use case |
|--------|-------------|----------|
| `vm-reset` | Crusoe API VM RESET + poll node Ready | **Default** — reboots the VM in-place |
| `vm-stop` | Crusoe API VM STOP + poll VM stopped | Stop a node without deleting |
| `vm-start` | Crusoe API VM START + poll node Ready | Start a stopped node |
| `vm-delete` | Crusoe API VM DELETE + wait for replacement | Replace a node (CMK auto-provisions new) |
| `noop` | No action | Testing — exercises cordon/drain/uncordon only |

### Multi-step Actions

```yaml
action:
  maxRetries: 3
  steps:
    - type: vm-stop
      timeout: 5m
    - type: vm-delete
      timeout: 30m
      wait: 2m    # wait 2m after this step before the next
```

---

## Drain Behavior

The tool follows [`kubectl drain`](https://kubernetes.io/docs/reference/generated/kubectl/kubectl-commands/#drain) semantics using the same `k8s.io/kubectl/pkg/drain` package. See the K8s docs on [safely draining a node](https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/) and [API-initiated eviction](https://kubernetes.io/docs/concepts/scheduling-eviction/api-eviction/) for background.

### Phase 1: Eviction (graceful)

Uses the [eviction API](https://kubernetes.io/docs/concepts/scheduling-eviction/api-eviction/) to evict pods, respecting [PodDisruptionBudgets](https://kubernetes.io/docs/tasks/run-application/configure-pod-disruption-budget/) and each pod's `terminationGracePeriodSeconds`. DaemonSet pods are skipped (`--ignore-daemonsets`).

### Phase 2: Force eviction (fallback)

Only runs if Phase 1 fails **and** `forceAfterEvictionFailure: true`. Retries with `Force=true` (bypasses PDBs) and `DeleteEmptyDirData=true` (deletes emptyDir data). The eviction API is still used — this ensures DaemonSet pods are properly skipped rather than causing delete errors.

```
Phase 1: Eviction (respects PDBs, graceful termination)
  ├── succeeds → continue to remediation action
  └── fails
      ├── forceAfterEvictionFailure: true  → Phase 2: Force eviction (bypass PDBs, delete emptyDir)
      │   ├── succeeds → continue to remediation action
      │   └── fails → leave cordoned, return error
      └── forceAfterEvictionFailure: false → leave cordoned, return error
```

---

## Cleanup

Remove the tool and restore all nodes:

```bash
# Uninstall Helm chart
helm uninstall crusoe-node-remediation -n crusoe-node-remediation

# Uncordon all managed nodes and remove labels/taints
for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
  kubectl uncordon "$node"
  kubectl label node "$node" crusoe.ai/remediation.managed-
  kubectl label node "$node" crusoe.ai/remediation.phase-
  kubectl taint node "$node" crusoe.ai/remediation.scheduled:NoSchedule-
  kubectl taint node "$node" crusoe.ai/remediation.draining:NoSchedule-
  kubectl taint node "$node" crusoe.ai/remediation.maintenance:NoSchedule-
done

# Delete namespace
kubectl delete namespace crusoe-node-remediation
```

---

## Building & Testing

```bash
# Build binary
make build

# Run tests
make test

# Build and push Docker image
make docker-build IMAGE_TAG=v0.0.11
make docker-push IMAGE_TAG=v0.0.11

# Validate config
./crusoe-node-remediation --validate
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     CronJob (every N min)                    │
│                                                              │
│  1. Discover nodes (label selector + instance type filter)  │
│  2. Query uptime (K8s stats summary API)                     │
│  3. Sort by highest uptime (closest to threshold first)      │
│  4. Check guardrails (global + per-pool limits)              │
│  5. For each eligible node:                                  │
│     a. Cordon (mark unschedulable)                           │
│     b. Drain (evict pods, skip DaemonSets)                   │
│     c. Execute action (vm-reset, noop, etc.)                │
│     d. Uncordon (back to schedulable)                        │
│     e. Set cooldown annotation                               │
│  6. Skip nodes in cooldown, at max retries, or running self  │
│                                                              │
│  Safety: self-node exclusion, guardrails, cooldown, retries  │
└─────────────────────────────────────────────────────────────┘
```

---

## RBAC

The tool requires these permissions:

| Resource | Verbs | Why |
|----------|-------|-----|
| `nodes` | get, list, watch, update, patch | Cordon/uncordon, labels, annotations |
| `nodes/proxy` | get | Stats summary API for uptime |
| `pods` | get, list, delete | Drain (evict pods) |
| `pods/eviction` | create | Drain (eviction API) |
| `daemonsets` (apps) | get, list | Drain (skip DaemonSet pods) |
| `poddisruptionbudgets` (policy) | get, list | Drain (check PDBs) |
| `events` | create, patch | Emit K8s events |

---

## FAQ

### Is it safe to run in production?

Yes — start with `dryRun: true` and `globalMaxCordoned: 1`. You'll only observe what would happen. Progress to `noop` action to test cordon/drain/uncordon, then to `vm-reset` for full remediation.

### What happens if drain fails?

If `forceAfterEvictionFailure: true`, the tool retries the [eviction](https://kubernetes.io/docs/concepts/scheduling-eviction/api-eviction/) with `Force=true` (bypasses [PDBs](https://kubernetes.io/docs/tasks/run-application/configure-pod-disruption-budget/)) and `DeleteEmptyDirData=true` (deletes emptyDir data). If force eviction also fails, the node is left cordoned and the error is logged. The next cron run will retry (up to `maxRetries`). See [safely draining a node](https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/) for K8s drain background.

### What happens after max retries?

The node is left cordoned with a `MaxRetriesExceeded` event. It will not be retried automatically. You need to manually uncordon it or investigate the issue.

### Can I target specific nodepools?

Yes — add `crusoe.ai/nodepool.id` to your `nodeSelector.matchLabels`:

```yaml
nodeSelector:
  matchLabels:
    crusoe.ai/project.id: "YOUR-PROJECT-ID"
    crusoe.ai/nodepool.id: "YOUR-NODEPOOL-ID"
```

### How does cooldown work?

After successful remediation, the `crusoe.ai/remediation.action-completed-at` annotation is set to the current time. The node is skipped on subsequent runs until `now - action-completed-at > remediationCooldown`.

### What if all nodes are cordoned?

The CronJob pod has tolerations for remediation taints and `node.kubernetes.io/unschedulable`, so it can still schedule on cordoned nodes. It also has required affinity to avoid nodes being actively remediated.

---

## License

Internal — Crusoe Cloud
