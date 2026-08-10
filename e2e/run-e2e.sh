#!/bin/bash
# E2E Test Script for cmk-node-remediation
#
# Prerequisites:
#   - Docker running
#   - kubectl configured for cmk-test-cluster
#   - crusoe CLI configured
#   - Helm installed
#
# Usage:
#   bash e2e/run-e2e.sh [phase]
#
# Phases:
#   1. build    — Build and push container image
#   2. deploy   — Create secret + install Helm chart (dry-run)
#   3. verify   — Check logs for correct node discovery
#   4. dry-run-off — Upgrade to dryRun=false (noop action)
#   5. vm-reset — Upgrade to vm-reset action (real API calls)
#   6. cleanup  — Uninstall Helm chart
#   7. all      — Run phases 1-4 in sequence

set -e

CHART_DIR="../helm/cmk-node-remediation"
NAMESPACE="cmk-node-remediation"
RELEASE_NAME="cmk-node-remediation"
PROJECT_ID="your-project-id"
REGISTRY="registry.your-location.ccr.crusoecloudcompute.com/crusoe-node-remediation.your-project-id"
IMAGE_TAG="v0.0.1"

PHASE="${1:-all}"

phase_build() {
    echo "=== Phase 1: Build and push container image ==="
    cd ..
    echo "Building image..."
    docker build -t "$REGISTRY:$IMAGE_TAG" .
    echo "Pushing image..."
    docker push "$REGISTRY:$IMAGE_TAG"
    cd e2e
    echo "✅ Image built and pushed"
}

phase_deploy() {
    echo "=== Phase 2: Deploy (dry-run mode) ==="

    # Create namespace
    kubectl create namespace "$NAMESPACE" 2>/dev/null || true

    # Create Crusoe API secret
    echo "Creating Crusoe API secret..."
    echo "You need to provide the Crusoe API key and URL."
    echo "API URL: https://api.cloud.crusoe.ai/v1alpha5"
    read -p "Enter Crusoe API key: " API_KEY
    kubectl create secret generic crusoe-api-credentials \
        --namespace "$NAMESPACE" \
        --from-literal=api-key="$API_KEY" \
        --from-literal=api-url="https://api.cloud.crusoe.ai/v1alpha5" \
        --dry-run=client -o yaml | kubectl apply -f -

    # Install Helm chart
    echo "Installing Helm chart (dry-run mode)..."
    helm upgrade --install "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f e2e/values-test-cluster.yaml

    echo "✅ Deployed in dry-run mode"
    echo "Waiting for first CronJob run..."
    echo "Check logs with: kubectl logs -n $NAMESPACE job/$(kubectl get jobs -n $NAMESPACE -o name | head -1)"
}

phase_verify() {
    echo "=== Phase 3: Verify node discovery ==="

    # Wait for first job to complete
    echo "Waiting for first job..."
    sleep 10

    # Get the latest job
    JOB=$(kubectl get jobs -n "$NAMESPACE" -o name 2>/dev/null | head -1)
    if [ -z "$JOB" ]; then
        echo "❌ No jobs found. Check CronJob status:"
        kubectl get cronjob -n "$NAMESPACE"
        exit 1
    fi

    echo "Latest job: $JOB"
    echo ""
    echo "=== Pod logs ==="
    kubectl logs -n "$NAMESPACE" "$JOB" 2>&1 || true
    echo ""

    echo "=== Nodes with remediation labels ==="
    kubectl get nodes -l crusoe.ai/remediation.managed=true 2>&1 || echo "No managed nodes (expected in dry-run)"
    echo ""

    echo "=== Events ==="
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -10
    echo ""

    echo "✅ Verify complete"
}

phase_dry_run_off() {
    echo "=== Phase 4: Upgrade to dryRun=false (noop action) ==="
    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f e2e/values-test-cluster-dry-run-off.yaml

    echo "✅ Upgraded to dryRun=false with noop action"
    echo "Waiting for next scheduled run..."
    echo "Check logs with: kubectl logs -n $NAMESPACE job/$(kubectl get jobs -n $NAMESPACE -o name | head -1)"
}

phase_vm_reset() {
    echo "=== Phase 5: Upgrade to vm-reset action ==="
    echo "⚠️  This will actually reset VMs in the test cluster!"
    read -p "Continue? (yes/no): " CONFIRM
    if [ "$CONFIRM" != "yes" ]; then
        echo "Aborted"
        exit 0
    fi

    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f e2e/values-test-cluster-vm-reset.yaml

    echo "✅ Upgraded to vm-reset action"
    echo "Monitoring required — check nodes and events"
}

phase_cleanup() {
    echo "=== Phase 6: Cleanup ==="
    helm uninstall "$RELEASE_NAME" --namespace "$NAMESPACE" 2>/dev/null || true
    kubectl delete namespace "$NAMESPACE" 2>/dev/null || true

    # Uncordon any nodes we cordoned
    echo "Uncordoning any managed nodes..."
    for node in $(kubectl get nodes -l crusoe.ai/remediation.managed=true -o name 2>/dev/null); do
        kubectl uncordon "$node" 2>/dev/null || true
        kubectl label "$node" crusoe.ai/remediation.managed- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.scheduled:NoSchedule- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.draining:NoSchedule- 2>/dev/null || true
        kubectl taint "$node" crusoe.ai/remediation.maintenance:NoSchedule- 2>/dev/null || true
    done

    echo "✅ Cleanup complete"
}

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
        echo "Usage: bash e2e/run-e2e.sh [build|deploy|verify|dry-run-off|vm-reset|cleanup|all]"
        exit 1
        ;;
esac
