package guardrails

import (
	"crusoe-cmk-node-remediation/internal/config"
)

// Checker enforces concurrency limits for cordoned/draining/remediation nodes.
type Checker struct {
	cfg config.GuardrailsConfig
}

func NewChecker(cfg config.GuardrailsConfig) *Checker {
	return &Checker{cfg: cfg}
}

// CanCordon returns true if a new node can be cordoned.
// globalActive: currently active nodes across cluster
// poolActive: currently active nodes in target pool
// globalTotal: total nodes in cluster
// poolTotal: total nodes in target pool
func (c *Checker) CanCordon(globalActive, poolActive, globalTotal, poolTotal int) bool {
	globalMax, poolMax := c.MaxAllowed(globalTotal, poolTotal)

	if globalActive >= globalMax {
		return false
	}
	if poolActive >= poolMax {
		return false
	}
	return true
}

// MaxAllowed returns the maximum allowed cordoned nodes at global and pool level.
func (c *Checker) MaxAllowed(globalTotal, poolTotal int) (globalMax, poolMax int) {
	globalMax = c.globalLimit(globalTotal)
	poolMax = c.poolLimit(poolTotal)
	return
}

func (c *Checker) globalLimit(globalTotal int) int {
	abs := c.cfg.GlobalMaxCordoned
	pct := -1
	if c.cfg.GlobalMaxCordonedPercent != nil {
		pct = (*c.cfg.GlobalMaxCordonedPercent * globalTotal) / 100
	}
	if abs > 0 && pct >= 0 {
		return min(abs, pct)
	}
	if abs > 0 {
		return abs
	}
	if pct >= 0 {
		return pct
	}
	return 0
}

func (c *Checker) poolLimit(poolTotal int) int {
	abs := c.cfg.PerPoolMaxCordoned
	pct := -1
	if c.cfg.PerPoolMaxCordonedPercent != nil {
		pct = (*c.cfg.PerPoolMaxCordonedPercent * poolTotal) / 100
	}
	if abs > 0 && pct >= 0 {
		return min(abs, pct)
	}
	if abs > 0 {
		return abs
	}
	if pct >= 0 {
		return pct
	}
	return 0
}
