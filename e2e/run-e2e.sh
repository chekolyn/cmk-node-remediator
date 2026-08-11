#!/bin/bash
# E2E Test Script for crusoe-node-remediation
#
# Usage:
#   CRUSOE_API_KEY="your-key" bash e2e/run-e2e.sh [phase] [--values FILE]
#
# Phases:
#   build       — Build and push container image
#   deploy      — Create secret + install Helm chart (dry-run)
#   verify      — Check logs for correct node discovery
#   dry-run-off — Upgrade to dryRun=false (noop action)
#   vm-reset    — Upgrade to vm-reset action (real API calls)
#   empirical   — Run vm-reset + collect timing data across multiple passes
#   cleanup     — Uninstall Helm chart, uncordon nodes
#   all         — Run build + deploy + verify in sequence
#
# Configuration:
#   Copy e2e/example-values.yaml to e2e/values.yaml and customize.
#   The values.yaml is gitignored — put your cluster details there.
#
# The API key is read from the CRUSOE_API_KEY environment variable.
# It is never written to disk or saved in shell history.
#
# Examples:
#   CRUSOE_ACCESS_KEY="your-access-key" CRUSOE_SECRET_KEY="your-secret-key" bash e2e/run-e2e.sh all
#   CRUSOE_ACCESS_KEY="your-access-key" CRUSOE_SECRET_KEY="your-secret-key" bash e2e/run-e2e.sh deploy --values e2e/values.yaml
#   bash e2e/run-e2e.sh cleanup  # no key needed

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

# ── Defaults ──────────────────────────────────────────────────────
CHART_DIR="$REPO_DIR/helm/crusoe-node-remediation"
NAMESPACE="crusoe-node-remediation"
RELEASE_NAME="crusoe-node-remediation"
VALUES_FILE="$SCRIPT_DIR/values.yaml"
CRUSOE_API_URL="https://api.cloud.crusoe.ai/v1alpha5"
CRUSOE_ACCESS_KEY="${CRUSOE_ACCESS_KEY:-}"
CRUSOE_SECRET_KEY="${CRUSOE_SECRET_KEY:-}"
PHASE="all"

# ── Parse args ────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
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
    if [ -z "$CRUSOE_ACCESS_KEY" ] || [ -z "$CRUSOE_SECRET_KEY" ]; then
        err "CRUSOE_ACCESS_KEY and CRUSOE_SECRET_KEY not set. Export them at runtime:\n  CRUSOE_ACCESS_KEY=\"your-access-key\" CRUSOE_SECRET_KEY=\"your-secret-key\" bash e2e/run-e2e.sh deploy"
    fi
    kubectl get namespace "$NAMESPACE" >/dev/null 2>&1 || \
        kubectl create namespace "$NAMESPACE"
    kubectl create secret generic crusoe-api-credentials \
        --namespace "$NAMESPACE" \
        --from-literal=access-key="$CRUSOE_ACCESS_KEY" \
        --from-literal=secret-key="$CRUSOE_SECRET_KEY" \
        --from-literal=api-url="$CRUSOE_API_URL" \
        --dry-run=client -o yaml | kubectl apply -f -

    # Create image pull secret for Crusoe Container Registry
    # Uses CRUSOE_REGISTRY_TOKEN env var (the full token string from `crusoe registry tokens create`)
    # and CRUSOE_REGISTRY_EMAIL (your Crusoe account email)
    if [ -n "${CRUSOE_REGISTRY_TOKEN:-}" ] && [ -n "${CRUSOE_REGISTRY_EMAIL:-}" ]; then
        local registry_server
        registry_server=$(echo "$VALUES_FILE" | xargs grep 'repository:' | head -1 | sed 's/.*repository: *//' | sed 's|/.*||' | tr -d '"')
        kubectl create secret docker-registry crusoe-registry-pull \
            --namespace "$NAMESPACE" \
            --docker-server="$registry_server" \
            --docker-username="$CRUSOE_REGISTRY_EMAIL" \
            --docker-password="$CRUSOE_REGISTRY_TOKEN" \
            --dry-run=client -o yaml | kubectl apply -f -
        log "Image pull secret ensured (idempotent)"
    fi

    log "Secrets ensured (idempotent)"
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
Copy e2e/example-values.yaml to e2e/values.yaml and customize:
  cp e2e/example-values.yaml e2e/values.yaml
Then edit values.yaml with your cluster details."
    fi

    log "Validating values: $VALUES_FILE"

    # Quick shell-level checks for required fields
    local errors=0

    local repo=$(grep 'repository:' "$VALUES_FILE" | head -1 | sed 's/.*repository: *//' | tr -d '"' | sed 's/#.*//')
    if [ -z "$repo" ] || [ "$repo" = '""' ]; then
        warn "image.repository is empty — set your Crusoe Container Registry URL"
        errors=$((errors + 1))
    fi

    local project_id=$(grep 'crusoeProjectId:' "$VALUES_FILE" | head -1 | sed 's/.*crusoeProjectId: *//' | tr -d '"' | sed 's/#.*//')
    if [ -z "$project_id" ] || [ "$project_id" = '""' ]; then
        warn "crusoeProjectId is empty — set your Crusoe project ID"
        errors=$((errors + 1))
    fi

    # Check nodeSelector has at least one label uncommented
    local selector_count=$(grep -v '^\s*#' "$VALUES_FILE" | grep -c 'crusoe.ai/' || true)
    if [ "$selector_count" -eq 0 ]; then
        warn "nodeSelector.matchLabels has no labels — uncomment and set crusoe.ai/project.id"
        errors=$((errors + 1))
    fi

    if [ $errors -gt 0 ]; then
        err "Values file has $errors issue(s) — fix before deploying."
    fi

    # Deep validation: render Helm chart → extract config.yaml → run binary --validate
    log "Running binary config validation..."
    local rendered=$(helm template "$RELEASE_NAME" "$CHART_DIR" -f "$VALUES_FILE" 2>&1)
    if [ $? -ne 0 ]; then
        err "Helm template failed:\n$rendered"
    fi

    # Extract config.yaml from the rendered ConfigMap
    local config_yaml=$(echo "$rendered" | awk '/^data:$/{found=1} found && /^  config.yaml: \|/{p=1; next} p && /^  [a-zA-Z]/{p=0} p{sub(/^    /,""); print}')

    if [ -z "$config_yaml" ]; then
        err "Could not extract config.yaml from rendered Helm output"
    fi

    # Write to temp file and validate with binary
    local tmp_config=$(mktemp /tmp/crusoe-node-remediation-config-XXXXXX.yaml)
    echo "$config_yaml" > "$tmp_config"

    # Build native binary for validation (cross-compiled binary won't run on host)
    local native_bin="$REPO_DIR/crusoe-node-remediation-validate"
    if [ ! -f "$native_bin" ]; then
        log "Building native binary for validation..."
        (cd "$REPO_DIR" && go build -o crusoe-node-remediation-validate ./cmd/manager)
    fi

    # Run binary with --validate
    if CONFIG_PATH="$tmp_config" "$native_bin" --validate 2>&1; then
        log "Binary config validation passed"
    else
        rm -f "$tmp_config"
        err "Binary config validation failed — check config.yaml output above"
    fi
    rm -f "$tmp_config"

    log "Values file OK: $VALUES_FILE"
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
    # Cross-compile binary for linux/amd64 (host may be arm64)
    log "Cross-compiling binary for linux/amd64..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o crusoe-node-remediation ./cmd/manager
    chmod +x crusoe-node-remediation
    # Build amd64 image and push (provenance=false avoids OCI manifest lists)
    docker buildx build --platform linux/amd64 --provenance=false -t "$full_image" --push .
    log "Image built and pushed: $full_image"
    cd "$SCRIPT_DIR"
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

    log "Pod logs"
    kubectl logs -n "$NAMESPACE" "$job" 2>&1 || true
    log "Nodes with remediation labels"
    kubectl get nodes -l crusoe.ai/remediation.managed=true 2>&1 || \
        echo "No managed nodes (expected in dry-run)"
    log "Events"
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -10
    log "CronJob status"
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
    kubectl logs -n "$NAMESPACE" "$(get_latest_job)" 2>&1 || true
    kubectl get nodes -l crusoe.ai/remediation.managed=true 2>&1 || true
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -10
}

phase_vm_reset() {
    log "Phase: Upgrade to vm-reset action"
    warn "This will actually reset VMs!"
    warn "Make sure action.type=vm-reset is set in $VALUES_FILE"
    read -p "Continue? (yes/no): " confirm
    [ "$confirm" != "yes" ] && { echo "Aborted"; exit 0; }

    check_values_file
    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE" \
        --set dryRun=false

    log "Upgraded — check $VALUES_FILE for action.type and forceAfterEvictionFailure settings"
    echo "Watch: kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp'"
    echo "Watch: kubectl get nodes -l crusoe.ai/remediation.managed=true -w"
}

phase_empirical() {
    log "Phase: Empirical data collection (vm-reset, multiple passes)"
    warn "This will actually reset VMs repeatedly!"
    read -p "Continue? (yes/no): " confirm
    [ "$confirm" != "yes" ] && { echo "Aborted"; exit 0; }

    check_values_file

    # Upgrade to vm-reset with aggressive settings from values.yaml
    helm upgrade "$RELEASE_NAME" "$CHART_DIR" \
        --namespace "$NAMESPACE" \
        -f "$VALUES_FILE" \
        --set dryRun=false

    log "Deployed with config from $VALUES_FILE"

    # Collect data across multiple passes
    local passes=${1:-5}
    local output_file="$SCRIPT_DIR/empirical-results-$(date +%Y%m%d-%H%M%S).md"

    # Read config from values.yaml for the report
    local sched=$(grep 'schedule:' "$VALUES_FILE" | head -1 | sed 's/.*schedule: *//' | tr -d '"')
    local cordon=$(grep 'cordonThreshold:' "$VALUES_FILE" | head -1 | sed 's/.*cordonThreshold: *//' | tr -d '"')
    local action=$(grep 'remediationThreshold:' "$VALUES_FILE" | head -1 | sed 's/.*remediationThreshold: *//' | tr -d '"')
    local cdn=$(grep 'remediationRemediationCooldown:' "$VALUES_FILE" | head -1 | sed 's/.*remediationRemediationCooldown: *//' | tr -d '"')
    local act_type=$(grep '  type:' "$VALUES_FILE" | head -1 | sed 's/.*type: *//' | tr -d '"')
    local gmax=$(grep 'globalMaxCordoned:' "$VALUES_FILE" | head -1 | sed 's/.*globalMaxCordoned: *//' | tr -d '"')
    local pmax=$(grep 'perPoolMaxCordoned:' "$VALUES_FILE" | head -1 | sed 's/.*perPoolMaxCordoned: *//' | tr -d '"')
    local drain=$(grep 'drainTimeout:' "$VALUES_FILE" | head -1 | sed 's/.*drainTimeout: *//' | tr -d '"')
    local force=$(grep 'forceAfterEvictionFailure:' "$VALUES_FILE" | head -1 | sed 's/.*forceAfterEvictionFailure: *//' | tr -d '"')

    cat > "$output_file" << EOF
# Empirical Test Results — $(date -u +%Y-%m-%dT%H:%M:%SZ)

## Configuration (from $VALUES_FILE)
- schedule: $sched
- cordonThreshold: $cordon
- remediationThreshold: $action
- remediationRemediationCooldown: $cdn
- action: $act_type
- guardrails: global=$gmax, perPool=$pmax
- drainTimeout: $drain
- forceAfterEvictionFailure: $force

## Passes
EOF

    for i in $(seq 1 "$passes"); do
        log "=== Pass $i/$passes ==="

        # Wait for next job
        local prev_job_count=$(kubectl get jobs -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d ' ')
        local waited=0
        echo "Waiting for new job..."
        while [ $waited -lt 180 ]; do
            local curr_count=$(kubectl get jobs -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d ' ')
            if [ "$curr_count" -gt "$prev_job_count" ]; then
                break
            fi
            sleep 5
            waited=$((waited + 5))
            echo -n "."
        done
        echo ""

        local job=$(kubectl get jobs -n "$NAMESPACE" --sort-by=.metadata.creationTimestamp --no-headers 2>/dev/null | tail -1 | awk '{print $1}')
        if [ -z "$job" ]; then
            warn "No job found for pass $i"
            continue
        fi

        local job_start=$(kubectl get job "$job" -n "$NAMESPACE" -o jsonpath='{.status.startTime}' 2>/dev/null)
        local pod=$(kubectl get pods -n "$NAMESPACE" -l job-name="$job" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

        echo "Job: $job (started: $job_start)"

        # Wait for job to complete
        kubectl wait --for=condition=complete "job/$job" -n "$NAMESPACE" --timeout=300s 2>/dev/null || true

        local job_complete=$(kubectl get job "$job" -n "$NAMESPACE" -o jsonpath='{.status.completionTime}' 2>/dev/null)
        local job_duration=""
        if [ -n "$job_start" ] && [ -n "$job_complete" ]; then
            local start_ts=$(date -d "$job_start" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$job_start" +%s 2>/dev/null || echo 0)
            local end_ts=$(date -d "$job_complete" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$job_complete" +%s 2>/dev/null || echo 0)
            if [ "$start_ts" -gt 0 ] && [ "$end_ts" -gt 0 ]; then
                job_duration=$((end_ts - start_ts))
            fi
        fi

        log "Pod logs (pass $i)"
        kubectl logs -n "$NAMESPACE" "$job" 2>&1 || true
        log "Node states (pass $i)"
        kubectl get nodes -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,AGE:.metadata.creationTimestamp,START:.status.nodeInfo.bootID' 2>&1 || true
        log "Remediation labels (pass $i)"
        kubectl get nodes -l crusoe.ai/remediation.managed=true -o custom-columns='NAME:.metadata.name,PHASE:.metadata.labels.crusoe\.ai/remediation\.phase,CORDONED:.spec.unschedulable' 2>&1 || true
        log "Events (pass $i)"
        kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 | tail -20

        # Record to output file
        cat >> "$output_file" << EOF

### Pass $i
- job: $job
- startTime: $job_start
- completionTime: $job_complete
EOF
        if [ -n "$job_duration" ]; then
            echo "- durationSeconds: $job_duration" >> "$output_file"
            echo "- durationHuman: $(printf '%dm%ds' $((job_duration/60)) $((job_duration%60)))" >> "$output_file"
        fi
        echo "" >> "$output_file"
        echo '```' >> "$output_file"
        kubectl logs -n "$NAMESPACE" "$job" 2>&1 >> "$output_file"
        echo '```' >> "$output_file"

        log "Pass $i complete (duration: ${job_duration:-unknown}s)"
        echo ""

        # Brief pause between passes
        if [ $i -lt "$passes" ]; then
            echo "Waiting 30s before next pass..."
            sleep 30
        fi
    done

    cat >> "$output_file" << 'EOF'

## Summary

Collected N passes of empirical data. Review the logs for:
- Time from cordon → drain → vm-reset API call
- Time from vm-reset API call → operation complete
- Time from operation complete → bootID change → node Ready
- Total end-to-end remediation time per node
- Guardrail behavior (how many nodes per pass)
- RemediationCooldown behavior (re-remediation prevention)
EOF
    sed -i '' "s/Collected N passes/Collected $passes passes/" "$output_file" 2>/dev/null || sed -i "s/Collected N passes/Collected $passes passes/" "$output_file"

    log "Empirical data collected: $output_file"
    cat "$output_file"
}

phase_cleanup() {
    log "Phase: Cleanup"
    helm uninstall "$RELEASE_NAME" --namespace "$NAMESPACE" 2>/dev/null || true
    log "Uncordoning managed nodes..."
    for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
        kubectl uncordon "$node" 2>/dev/null || true
        kubectl label node "$node" crusoe.ai/remediation.managed- 2>/dev/null || true
        kubectl label node "$node" crusoe.ai/remediation.phase- 2>/dev/null || true
        kubectl taint node "$node" crusoe.ai/remediation.scheduled:NoSchedule- 2>/dev/null || true
        kubectl taint node "$node" crusoe.ai/remediation.draining:NoSchedule- 2>/dev/null || true
        kubectl taint node "$node" crusoe.ai/remediation.maintenance:NoSchedule- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.attempt- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.action-completed-at- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.cordoned-at- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.drain-started-at- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.force-drain-started-at- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.uncordoned-at- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.last-action- 2>/dev/null || true
        kubectl annotate node "$node" crusoe.ai/remediation.cordon-uptime- 2>/dev/null || true
        echo "  Cleaned: $node"
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
        empirical)   phase_empirical "${@:1}" ;;
        cleanup)     phase_cleanup ;;
        all)         phase_build; phase_deploy; phase_verify ;;
        *)
            echo "Usage: bash e2e/run-e2e.sh [build|deploy|verify|dry-run-off|vm-reset|empirical|cleanup|all]"
            echo "       [--values FILE] [--namespace NS]"
            echo ""
            echo "  Set CRUSOE_API_KEY env var before running (never passed as CLI arg):"
            echo "    CRUSOE_API_KEY=\"your-key\" bash e2e/run-e2e.sh deploy"
            echo ""
            echo "  empirical [N]  — Run N passes of vm-reset with timing data (default 5)"
            echo ""
            echo "Setup: cp e2e/example-values.yaml e2e/values.yaml  (then edit)"
            exit 1
            ;;
    esac
