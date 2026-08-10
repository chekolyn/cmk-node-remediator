package lifecycle

// ActionTier classifies what action to take based on uptime.
type ActionTier int

const (
	ActionNone      ActionTier = iota // < cordonDays
	ActionCordon                      // cordonDays <= days < actionDays
	ActionRemediate                    // >= actionDays
)

// ClassifyUptime determines the action tier for a node based on its uptime in days.
func ClassifyUptime(days float64, cordonDays, actionDays int) ActionTier {
	if days >= float64(actionDays) {
		return ActionRemediate
	}
	if days >= float64(cordonDays) {
		return ActionCordon
	}
	return ActionNone
}
