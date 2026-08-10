package guardrails

import (
	"testing"

	"cmk-node-remediation/internal/config"
)

func TestCanCordonAbsoluteLimits(t *testing.T) {
	cfg := config.GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2}
	checker := NewChecker(cfg)

	if !checker.CanCordon(4, 1, 100, 100) {
		t.Error("CanCordon(4, 1, 100, 100) = false, want true")
	}
	if checker.CanCordon(5, 1, 100, 100) {
		t.Error("CanCordon(5, 1, 100, 100) = true, want false (global limit)")
	}
	if checker.CanCordon(3, 2, 100, 100) {
		t.Error("CanCordon(3, 2, 100, 100) = true, want false (pool limit)")
	}
}

func TestCanCordonPercentLimits(t *testing.T) {
	globalPct := 20
	poolPct := 25
	cfg := config.GuardrailsConfig{
		GlobalMaxCordonedPercent:  &globalPct,
		PerPoolMaxCordonedPercent: &poolPct,
	}
	checker := NewChecker(cfg)

	if !checker.CanCordon(19, 24, 100, 100) {
		t.Error("CanCordon(19, 24, 100, 100) = false, want true")
	}
	if checker.CanCordon(20, 10, 100, 100) {
		t.Error("CanCordon(20, 10, 100, 100) = true, want false (global % limit)")
	}
}

func TestCanCordonGlobalPercentWithDifferentTotals(t *testing.T) {
	globalPct := 10
	cfg := config.GuardrailsConfig{
		GlobalMaxCordonedPercent: &globalPct,
		PerPoolMaxCordoned:       5,
	}
	checker := NewChecker(cfg)

	// 458 global total (like a large customer), 50 pool total
	// 10% of 458 = 45 global max; 5 pool max
	if !checker.CanCordon(44, 4, 458, 50) {
		t.Error("CanCordon(44, 4, 458, 50) = false, want true")
	}
	if checker.CanCordon(45, 4, 458, 50) {
		t.Error("CanCordon(45, 4, 458, 50) = true, want false (global % limit)")
	}
}

func TestMaxAllowed(t *testing.T) {
	globalPct := 20
	poolPct := 25
	cfg := config.GuardrailsConfig{
		GlobalMaxCordoned:         5,
		GlobalMaxCordonedPercent:  &globalPct,
		PerPoolMaxCordoned:        2,
		PerPoolMaxCordonedPercent: &poolPct,
	}
	checker := NewChecker(cfg)

	gMax, pMax := checker.MaxAllowed(100, 100)
	if gMax != 5 {
		t.Errorf("globalMax = %d, want 5", gMax)
	}
	if pMax != 2 {
		t.Errorf("poolMax = %d, want 2", pMax)
	}

	gMax, pMax = checker.MaxAllowed(10, 8)
	if gMax != 2 {
		t.Errorf("globalMax = %d, want 2", gMax)
	}
	if pMax != 2 {
		t.Errorf("poolMax = %d, want 2", pMax)
	}
}
