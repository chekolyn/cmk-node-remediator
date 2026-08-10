package lifecycle

import (
	"testing"

	"cmk-node-remediation/internal/constants"
)

func TestClassifyUptime(t *testing.T) {
	tests := []struct {
		name       string
		days       float64
		cordonDays int
		actionDays int
		want       ActionTier
	}{
		{"below cordon", 40.0, 55, 60, ActionNone},
		{"at cordon", 55.0, 55, 60, ActionCordon},
		{"between", 57.5, 55, 60, ActionCordon},
		{"at action", 60.0, 55, 60, ActionRemediate},
		{"above action", 65.0, 55, 60, ActionRemediate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyUptime(tt.days, tt.cordonDays, tt.actionDays)
			if got != tt.want {
				t.Errorf("ClassifyUptime(%.1f, %d, %d) = %v, want %v",
					tt.days, tt.cordonDays, tt.actionDays, got, tt.want)
			}
		})
	}
}

func TestPhaseIsActive(t *testing.T) {
	active := []constants.Phase{
		constants.PhaseCordoned, constants.PhaseDraining, constants.PhaseForceDraining,
		constants.PhaseActionRunning, constants.PhaseUncordoning,
	}
	for _, p := range active {
		if !p.IsActive() {
			t.Errorf("Phase %v should be active", p)
		}
	}
	if constants.PhaseMonitored.IsActive() {
		t.Error("PhaseMonitored should not be active")
	}
}

func TestTaintKeyForPhase(t *testing.T) {
	tests := []struct {
		phase constants.Phase
		want  string
	}{
		{constants.PhaseCordoned, constants.TaintScheduled},
		{constants.PhaseDraining, constants.TaintDraining},
		{constants.PhaseForceDraining, constants.TaintDraining},
		{constants.PhaseActionRunning, constants.TaintMaintenance},
		{constants.PhaseUncordoning, constants.TaintScheduled},
	}
	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			got := constants.TaintKeyForPhase(tt.phase)
			if got != tt.want {
				t.Errorf("TaintKeyForPhase(%v) = %q, want %q", tt.phase, got, tt.want)
			}
		})
	}
}
