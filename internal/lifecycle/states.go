package lifecycle

import (
	"time"
)

// ActionTier classifies what action to take based on uptime.
type ActionTier int

const (
	ActionNone      ActionTier = iota // < cordonThreshold
	ActionCordon                      // cordonThreshold <= uptime < actionThreshold
	ActionRemediate                    // >= actionThreshold
)

// ClassifyUptime determines the action tier for a node based on its uptime.
// All thresholds are time.Duration — supports days, hours, or minutes.
func ClassifyUptime(uptime time.Duration, cordonThreshold, actionThreshold time.Duration) ActionTier {
	if uptime >= actionThreshold {
		return ActionRemediate
	}
	if uptime >= cordonThreshold {
		return ActionCordon
	}
	return ActionNone
}
