#!/bin/bash
# E2E Test Script for cmk-node-remediation
#
# Usage:
#   bash e2e/run-e2e.sh [phase] [--api-key KEY] [--values FILE]
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
#   Copy e2e/values.yaml to e2e/local-values.yaml and customize.
#   The local-values.yaml is gitignored — put your cluster details there.
#
# Examples:
#   bash e2e/run-e2e.sh all --api-key YOUR_KEY
#   bash e2e/run-e2e.sh deploy --values e2e/local-values.yaml --api-key YOUR_KEY
#   bash e2e/run-e2e.sh cleanup

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

# ── Defaults ──────────────────────────────────────────────────────
CHART_DIR="$REPO_DIR/helm/cmk-node-remediation"
NAMESPACE="cmk-node-remediation"
RELEASE_NAME="cmk-node-remediation"
VALUES_FILE="$SCRIPT_DIR/local-values.yaml"
CRUSOE_API_URL="https://api.cloud.crusoe.ai/v1alpha5"
CRUSOE_API_KEY=""
PHASE="all"

# ── Parse args ────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --api-key)  CRUSOE_API_KEY="$2"; shift 2 ;;
        --values)   VALUES_FILE="$2"; shift 2 ;;
        --namespace) NAMESPACE="$2"; shift 2 ;;
        *)          PHASE="$1"; shift ;;
    esac
done

# ── Helpers ──────────────────────────────────────────────────────
log()  { echo -e "\033[0;32m=== $1 ===\033[0m"; }
warn() { echo -e "\033[0;33m⚠️  $1\033[0m"; }
err()  { echo -e "\033[0;31m❌ $1\033[0m"; exit 1; }

check_prereqs() {
    command -v docker  >/dev/null || err "docker not found"
    command -v kubectl >/dev/null || err "kubectl not found"
    command -v helm    >/dev/null || err "helm not found"
}

ensure_secret() {
    if [ -z "$CRUSOE_API_KEY" ]; then
        err "CRUSOE_API_KEY not set. Use --api-key or export CRUSOE_API_KEY."
    fi
    kubectl get namespace "$NAMESPACE" >/dev/null 2>&1 || \
        kubectl create namespace "$NAMESPACE"
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

check_values_file() {
    if [ ! -f "$VALUES_FILE" ]; then
        err "Values file not found: $VALUES_FILE
Copy e2e/values.yaml to e2e/local-values.yaml and customize:
  cp e2e/values.yaml e2e/local-values.yaml
Then edit local-values.yaml with your cluster details."
    fi
    log "Using values: $VALUES_FILE"
}

# ── Phases ────────────────────────────────────────────────────────

phase_build() {
    log "Phase: Build and push container image"
    check_prereqs
    check_values_file

    # Extract image repo and tag from values file
    local repo=$(grep 'repository:' "$VALUES_FILE" | head -1 | sed 's/.*repository: *//' | tr -d '"')
    local tag=$(grep 'tag:' "$VALUES_FILE" | head -1 | sed 's/.*tag: *//' | tr -d '"')
    if [ -z "$repo" ]; then
        err "image.repository not set in $VALUES_FILE"
    fi
    local full_image="${repo}:${tag}"

    echo "Building: $full_image"
    cd "$REPO_DIR"
    docker build -t "$full_image" .
    echo "Pushing..."
    docker push "$full_image"
    cd "$SCRIPT_DIR"
    log "Image built and pushed: $full_image"
}

phase_deploy() {
    log "Phase: Deploy (dry-run mode)"
    check_prereqs
    check_values_file
    ensure_secret

    helm upgrade --install "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE"

    log "Deployed — waiting for first run"
    wait_for_job
}

phase_verify() {
    log "Phase: Verify node discovery"
    local job=$(get_latest_job)
    if [ -z "$job" ]; then
        warn "No jobs found. Triggering manually..."
        kubectl create job --from=cronjob/"$RELEASE_NAME" \
            "manual-$(date +%s)" -n "$NAMESPACE"
        sleep 5
        job=$(get_latest_job)
    fi
    [ -z "$job" ] && err "No job found"

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
    log "Verify complete"
}

phase_dry_run_off() {
    log "Phase: Upgrade to dryRun=false (noop)"
    check_values_file

    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE" \
        --set dryRun=false

    log "Upgraded to dryRun=false"
    wait_for_job
    echo ""
    kubectl logs -n "$NAMESPACE" "$(get_latest_job)" 2>&1 || true
    echo ""
    kubectl get nodes -l crusoe.ai/remediation.managed=true 2>&1 || true
    echo ""
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -10
}

phase_vm_reset() {
    log "Phase: Upgrade to vm-reset action"
    warn "This will actually reset VMs!"
    read -p "Continue? (yes/no): " confirm
    [ "$confirm" != "yes" ] && { echo "Aborted"; exit 0; }

    check_values_file
    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE" \
        --set dryRun=false \
        --set action.type=vm-reset \
        --set 'schedule=*/10 * * * *' \
        --set guardrails.globalMaxCordoned=1 \
        --set guardrails.perPoolMaxCordoned=1

    log "Upgraded to vm-reset"
    echo "Watch: kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp'"
    echo "Watch: kubectl get nodes -l crusoe.ai/remediation.managed=true -w"
}

phase_cleanup() {
    log "Phase: Cleanup"
    helm uninstall "$RELEASE_NAME" --namespace "$NAMESPACE" 2>/dev/null || true
    echo "Uncordoning managed nodes..."
    for node in $(kubectl get nodes -l crusoe.ai/remediation.managed=true -o name 2>/dev/null); do
        kubectl uncordon "$node" 2>/dev/null || true
        kubectl label "$node" crusoe.ai/remediation.managed- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.scheduled:NoSchedule- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.draining:NoSchedule- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.maintenance:NoSchedule- 2>/dev/null || true
        echo "  Uncordoned: $node"
    done
    kubectl delete namespace "$NAMESPACE" 2>/dev/null || true
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
    all)         phase_build; phase_deploy; phase_verify ;;
    *)
        echo "Usage: bash e2e/run-e2e.sh [build|deploy|verify|dry-run-off|vm-reset|cleanup|all]"
        echo "       [--api-key KEY] [--values FILE] [--namespace NS]"
        echo ""
        echo "Setup: cp e2e/values.yaml e2e/local-values.yaml  (then edit)"
        exit 1
        ;;
esac
