#!/bin/bash
# TDD Test: Helm template renders valid YAML
# Run: bash scripts/helm-template-test.sh

set -e

CHART_DIR="helm/crusoe-node-remediation"
FAIL=0

echo "=== Test 1: Default values render without error ==="
if helm template "$CHART_DIR" --set crusoeProjectId=test-project > /dev/null 2>&1; then
    echo "PASS"
else
    echo "FAIL: default values don't render"
    helm template "$CHART_DIR" --set crusoeProjectId=test-project 2>&1
    FAIL=1
fi

echo "=== Test 2: Dry-run mode renders ==="
if helm template "$CHART_DIR" --set crusoeProjectId=test-project --set dryRun=true > /dev/null 2>&1; then
    echo "PASS"
else
    echo "FAIL: dry-run mode doesn't render"
    FAIL=1
fi

echo "=== Test 3: Multi-step action renders ==="
if helm template "$CHART_DIR" --set crusoeProjectId=test-project \
  --set-json 'action.steps=[{"type":"vm-stop","timeout":"5m"},{"type":"vm-delete","timeout":"30m","wait":"2m"}]' \
  --set action.type="" > /dev/null 2>&1; then
    echo "PASS"
else
    echo "FAIL: multi-step action doesn't render"
    FAIL=1
fi

echo "=== Test 4: ClusterRole has nodes/proxy ==="
OUTPUT=$(helm template "$CHART_DIR" --set crusoeProjectId=test-project 2>&1)
if echo "$OUTPUT" | grep -q "nodes/proxy"; then
    echo "PASS"
else
    echo "FAIL: ClusterRole missing nodes/proxy"
    FAIL=1
fi

echo "=== Test 5: CronJob has concurrencyPolicy: Forbid ==="
if echo "$OUTPUT" | grep -q "concurrencyPolicy: Forbid"; then
    echo "PASS"
else
    echo "FAIL: CronJob missing concurrencyPolicy: Forbid"
    FAIL=1
fi

echo "=== Test 6: No Secret template rendered ==="
if echo "$OUTPUT" | grep -q "kind: Secret"; then
    echo "FAIL: Secret template should not be rendered"
    FAIL=1
else
    echo "PASS"
fi

echo "=== Test 7: Security context present ==="
if echo "$OUTPUT" | grep -q "runAsNonRoot: true"; then
    echo "PASS"
else
    echo "FAIL: securityContext missing"
    FAIL=1
fi

if [ $FAIL -eq 0 ]; then
    echo ""
    echo "All Helm template tests PASSED"
    exit 0
else
    echo ""
    echo "Some Helm template tests FAILED"
    exit 1
fi
