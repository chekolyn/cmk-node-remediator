#!/bin/bash
# E2E Test Script for cmk-node-remediation
#
# Usage:
#   bash e2e/run-e2e.sh [phase] [--api-key KEY]
#
# Phases:
#   build       — Build and push container image
#   deploy      — Create secret + install Helm chart (dry-run)
#   verify      — Check logs for correct node discovery
#   dry-run-off — Upgrade to dryRun=false (noop action)
#   vm-reset    — Upgrade to vm-reset action (real API calls)
#   cleanup     — Uninstall Helm chart, uncordon nodes
#   all         — Run build + deploy + verify in sequence
#
# Configuration:
#   Copy e2e/config.env to e2e/local.env and customize.
#   Or export env vars before running.
#
# Examples:
#   bash e2e/run-e2e.sh all
#   bash e2e/run-e2e.sh deploy --api-key mykey123
#   CLUSTER_NAME=example-gpu-cluster bash e2e/run-e2e.sh deploy

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

# ── Load configuration ─────────────────────────────────────────────
# Source config.env for defaults, then local.env for overrides
source "$SCRIPT_DIR/config.env"
if [ -f "$SCRIPT_DIR/local.env" ]; then
    source "$SCRIPT_DIR/local.env"
fi

# Parse --api-key flag
API_KEY_OVERRIDE=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --api-key)
            API_KEY_OVERRIDE="$2"
            shift 2
            ;;
        *)
            PHASE="$1"
            shift
            ;;
    esac
done
if [ -n "$API_KEY_OVERRIDE" ]; then
    CRUSOE_API_KEY="$API_KEY_OVERRIDE"
fi

PHASE="${PHASE:-all}"

# ── Derived values ────────────────────────────────────────────────
FULL_IMAGE="${REGISTRY_URL}/${REGISTRY_REPO}:${IMAGE_TAG}"
CHART_DIR="$REPO_DIR/helm/cmk-node-remediation"
VALUES_FILE="$SCRIPT_DIR/values-generated.yaml"

# ── Helpers ──────────────────────────────────────────────────────
log()  { echo -e "\033[0;32m=== $1 ===\033[0m"; }
warn() { echo -e "\033[0;33m⚠️  $1\033[0m"; }
err()  { echo -e "\033[0;31m❌ $1\033[0m"; exit 1; }

check_prereqs() {
    command -v docker  >/dev/null || err "docker not found"
    command -v kubectl >/dev/null || err "kubectl not found"
    command -v helm    >/dev/null || err "helm not found"
    command -v crusoe  >/dev/null || err "crusoe CLI not found"
}

generate_values() {
    local dry_run="${1:-true}"
    local action_type="${2:-noop}"
    local schedule="${3:-$SCHEDULE}"
    local global_max="${4:-$GLOBAL_MAX_CORDONED}"
    local per_pool_max="${5:-$PER_POOL_MAX_CORDONED}"

    cat > "$VALUES_FILE" <<EOF
image:
  repository: "${REGISTRY_URL}/${REGISTRY_REPO}"
  tag: "${IMAGE_TAG}"
  pullPolicy: IfNotPresent

schedule: "${schedule}"

nodeSelector:
  matchLabels:
    crusoe.ai/project.id: "${PROJECT_ID}"

vmInstanceTypeFilter: "${VM_INSTANCE_TYPE_FILTER}"

cordonThreshold: ${CORDON_THRESHOLD}
actionThreshold: ${ACTION_THRESHOLD}
cooldown: ${COOLDOWN}

action:
  type: ${action_type}
  timeout: 10m
  maxRetries: 2

guardrails:
  globalMaxCordoned: ${global_max}
  perPoolMaxCordoned: ${per_pool_max}

drainTimeout: 2m
forceDrainAfterTimeout: true

crusoeApiSecret: crusoe-api-credentials
crusoeProjectId: "${PROJECT_ID}"

dryRun: ${dry_run}

activeDeadlineSeconds: 600
failedJobsHistoryLimit: 5
successfulJobsHistoryLimit: 3
EOF
}

ensure_secret() {
    if [ -z "$CRUSOE_API_KEY" ]; then
        err "CRUSOE_API_KEY not set. Use --api-key or export CRUSOE_API_KEY."
    fi

    kubectl get namespace "$NAMESPACE" >/dev/null 2>&1 || \
        kubectl create namespace "$NAMESPACE"

    # Idempotent: create or update secret
    kubectl create secret generic crusoe-api-credentials \
        --namespace "$NAMESPACE" \
        --from-literal=api-key="$CRUSOE_API_KEY" \
        --from-literal=api-url="$CRUSOE_API_URL" \
        --dry-run=client -o yaml | kubectl apply -f -
    log "Secret ensured (idempotent)"
}

get_latest_job() {
    kubectl get jobs -n "$NAMESPACE" -o name 2>/dev/null | head -1
}

wait_for_job() {
    local timeout=120
    local elapsed=0
    log "Waiting for CronJob to trigger (up to ${timeout}s)..."
    while [ $elapsed -lt $timeout ]; do
        local job=$(get_latest_job)
        if [ -n "$job" ]; then
            echo "Found job: $job"
            # Wait for job to complete
            kubectl wait --for=condition=complete "$job" \
                -n "$NAMESPACE" --timeout=120s 2>/dev/null || true
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
        echo -n "."
    done
    echo ""
    warn "No job found within ${timeout}s"
    return 1
}

# ── Phases ────────────────────────────────────────────────────────

phase_build() {
    log "Phase: Build and push container image"
    check_prereqs

    echo "Building image: $FULL_IMAGE"
    cd "$REPO_DIR"
    docker build -t "$FULL_IMAGE" .

    echo "Pushing image..."
    docker push "$FULL_IMAGE"
    cd "$SCRIPT_DIR"
    log "Image built and pushed: $FULL_IMAGE"
}

phase_deploy() {
    log "Phase: Deploy (dry-run mode)"
    check_prereqs

    ensure_secret

    generate_values "true" "noop"
    echo "Values file: $VALUES_FILE"
    echo ""

    # Idempotent: upgrade or install
    helm upgrade --install "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE"

    log "Deployed in dry-run mode"
    echo "Schedule: $SCHEDULE (every 5 min)"
    echo "Waiting for first run..."
    wait_for_job
}

phase_verify() {
    log "Phase: Verify node discovery"

    local job=$(get_latest_job)
    if [ -z "$job" ]; then
        warn "No jobs found. Triggering manually..."
        kubectl create job --from=cronjob/"$RELEASE_NAME" \
            "manual-trigger-$(date +%s)" -n "$NAMESPACE"
        sleep 5
        job=$(get_latest_job)
    fi

    if [ -z "$job" ]; then
        err "No job found after manual trigger"
    fi

    echo ""
    echo "=== Pod logs ==="
    kubectl logs -n "$NAMESPACE" "$job" 2>&1 || true
    echo ""

    echo "=== Nodes with remediation labels ==="
    kubectl get nodes -l crusoe.ai/remediation.managed=true 2>&1 || \
        echo "No managed nodes (expected in dry-run)"
    echo ""

    echo "=== Events ==="
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -10
    echo ""

    echo "=== CronJob status ==="
    kubectl get cronjob -n "$NAMESPACE"
    echo ""

    log "Verify complete — check logs above for correct node targeting"
}

phase_dry_run_off() {
    log "Phase: Upgrade to dryRun=false (noop action)"

    generate_values "false" "noop"

    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE"

    log "Upgraded to dryRun=false with noop action"
    wait_for_job

    echo ""
    echo "=== Pod logs ==="
    kubectl logs -n "$NAMESPACE" "$(get_latest_job)" 2>&1 || true
    echo ""

    echo "=== Nodes with remediation labels ==="
    kubectl get nodes -l crusoe.ai/remediation.managed=true 2>&1 || true
    echo ""

    echo "=== Events ==="
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -10
}

phase_vm_reset() {
    log "Phase: Upgrade to vm-reset action"
    warn "This will actually reset VMs in cluster: $CLUSTER_NAME"
    read -p "Continue? (yes/no): " confirm
    if [ "$confirm" != "yes" ]; then
        echo "Aborted"
        exit 0
    fi

    generate_values "false" "vm-reset" "*/10 * * * *" "1" "1"

    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE"

    log "Upgraded to vm-reset action"
    echo "Monitoring required — check nodes and events"
    echo ""
    echo "Watch: kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp'"
    echo "Watch: kubectl get nodes -l crusoe.ai/remediation.managed=true -w"
}

phase_cleanup() {
    log "Phase: Cleanup"

    helm uninstall "$RELEASE_NAME" --namespace "$NAMESPACE" 2>/dev/null || true

    # Uncordon any nodes we cordoned
    echo "Uncordoning any managed nodes..."
    for node in $(kubectl get nodes -l crusoe.ai/remediation.managed=true -o name 2>/dev/null); do
        kubectl uncordon "$node" 2>/dev/null || true
        kubectl label "$node" crusoe.ai/remediation.managed- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.scheduled:NoSchedule- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.draining:NoSchedule- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.maintenance:NoSchedule- 2>/dev/null || true
        echo "  Uncordoned: $node"
    done

    kubectl delete namespace "$NAMESPACE" 2>/dev/null || true
    rm -f "$VALUES_FILE"
    log "Cleanup complete"
}

# ── Main ─────────────────────────────────────────────────────────

case "$PHASE" in
    build)       phase_build ;;
    deploy)      phase_deploy ;;
    verify)      phase_verify ;;
    dry-run-off) phase_dry_run_off ;;
    vm-reset)    phase_vm_reset ;;
    cleanup)     phase_cleanup ;;
    all)
        phase_build
        phase_deploy
        phase_verify
        ;;
    *)
        echo "Usage: bash e2e/run-e2e.sh [build|deploy|verify|dry-run-off|vm-reset|cleanup|all] [--api-key KEY]"
        echo ""
        echo "Configuration: copy e2e/config.env to e2e/local.env and customize."
        echo "Or export env vars: CLUSTER_NAME, PROJECT_ID, CRUSOE_API_KEY, etc."
        exit 1
        ;;
esac
