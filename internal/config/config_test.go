package config

import (
	"testing"
	"time"
)

func TestLoadConfigFromYAML(t *testing.T) {
	yamlData := []byte(`
schedule: "0 0 * * *"
vmInstanceTypeFilter: "b200.*"
cordonThreshold: 55d
actionThreshold: 60d
cooldown: 7d
action:
  type: vm-reset
  timeout: 30m
  maxRetries: 3
guardrails:
  globalMaxCordoned: 5
  perPoolMaxCordoned: 2
drainTimeout: 10m
forceDrainAfterTimeout: true
crusoeApiSecret: crusoe-api-credentials
crusoeProjectId: your-project-id
dryRun: false
nodeSelector:
  matchLabels:
    crusoe.ai/project.id: your-project-id
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.CordonThreshold != Duration(55*24*time.Hour) {
		t.Errorf("CordonThreshold = %v, want 55d", cfg.CordonThreshold)
	}
	if cfg.ActionThreshold != Duration(60*24*time.Hour) {
		t.Errorf("ActionThreshold = %v, want 60d", cfg.ActionThreshold)
	}
	if cfg.Cooldown != Duration(7*24*time.Hour) {
		t.Errorf("Cooldown = %v, want 7d", cfg.Cooldown)
	}
	if cfg.Action.Type != "vm-reset" {
		t.Errorf("Action.Type = %q, want %q", cfg.Action.Type, "vm-reset")
	}
	if cfg.Action.Timeout != 30*time.Minute {
		t.Errorf("Action.Timeout = %v, want 30m", cfg.Action.Timeout)
	}
	if cfg.Action.MaxRetries != 3 {
		t.Errorf("Action.MaxRetries = %d, want 3", cfg.Action.MaxRetries)
	}
	if cfg.Guardrails.GlobalMaxCordoned != 5 {
		t.Errorf("GlobalMaxCordoned = %d, want 5", cfg.Guardrails.GlobalMaxCordoned)
	}
	if cfg.DrainTimeout != 10*time.Minute {
		t.Errorf("DrainTimeout = %v, want 10m", cfg.DrainTimeout)
	}
	if !cfg.ForceDrainAfterTimeout {
		t.Errorf("ForceDrainAfterTimeout = false, want true")
	}
}

func TestLoadConfigHoursAndMinutes(t *testing.T) {
	// Test that hours and minutes work as thresholds
	yamlData := []byte(`
cordonThreshold: 30m
actionThreshold: 1h
cooldown: 15m
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.CordonThreshold != Duration(30*time.Minute) {
		t.Errorf("CordonThreshold = %v, want 30m", cfg.CordonThreshold)
	}
	if cfg.ActionThreshold != Duration(1*time.Hour) {
		t.Errorf("ActionThreshold = %v, want 1h", cfg.ActionThreshold)
	}
	if cfg.Cooldown != Duration(15*time.Minute) {
		t.Errorf("Cooldown = %v, want 15m", cfg.Cooldown)
	}
}

func TestLoadConfigMixedUnits(t *testing.T) {
	// Test that 1h can be expressed as 60m — they should be equal
	yamlData1 := []byte(`
cordonThreshold: 1h
actionThreshold: 2h
cooldown: 30m
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	yamlData2 := []byte(`
cordonThreshold: 60m
actionThreshold: 120m
cooldown: 1800s
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	cfg1, err := LoadFromYAML(yamlData1)
	if err != nil {
		t.Fatalf("LoadFromYAML (1h) failed: %v", err)
	}

	cfg2, err := LoadFromYAML(yamlData2)
	if err != nil {
		t.Fatalf("LoadFromYAML (60m) failed: %v", err)
	}

	if cfg1.CordonThreshold != cfg2.CordonThreshold {
		t.Errorf("1h = %v but 60m = %v — should be equal", cfg1.CordonThreshold, cfg2.CordonThreshold)
	}
	if cfg1.ActionThreshold != cfg2.ActionThreshold {
		t.Errorf("2h = %v but 120m = %v — should be equal", cfg1.ActionThreshold, cfg2.ActionThreshold)
	}
	if cfg1.Cooldown != cfg2.Cooldown {
		t.Errorf("30m = %v but 1800s = %v — should be equal", cfg1.Cooldown, cfg2.Cooldown)
	}
}

func TestLoadConfigMultiStepAction(t *testing.T) {
	yamlData := []byte(`
cordonThreshold: 55d
actionThreshold: 60d
action:
  maxRetries: 3
  steps:
    - type: vm-stop
      timeout: 5m
    - type: vm-delete
      timeout: 30m
      wait: 2m
crusoeApiSecret: crusoe-api-credentials
crusoeProjectId: proj-1
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if len(cfg.Action.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(cfg.Action.Steps))
	}
	if cfg.Action.Steps[0].Type != "vm-stop" {
		t.Errorf("step 0 type = %q, want vm-stop", cfg.Action.Steps[0].Type)
	}
	if cfg.Action.Steps[1].Type != "vm-delete" {
		t.Errorf("step 1 type = %q, want vm-delete", cfg.Action.Steps[1].Type)
	}
	if cfg.Action.Steps[1].Wait != 2*time.Minute {
		t.Errorf("step 1 wait = %v, want 2m", cfg.Action.Steps[1].Wait)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadFromYAML([]byte(`{}`))
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.Schedule != "0 0 * * *" {
		t.Errorf("default Schedule = %q, want %q", cfg.Schedule, "0 0 * * *")
	}
	if cfg.CordonThreshold != Duration(55*24*time.Hour) {
		t.Errorf("default CordonThreshold = %v, want 55d", cfg.CordonThreshold)
	}
	if cfg.ActionThreshold != Duration(60*24*time.Hour) {
		t.Errorf("default ActionThreshold = %v, want 60d", cfg.ActionThreshold)
	}
	if cfg.Cooldown != Duration(7*24*time.Hour) {
		t.Errorf("default Cooldown = %v, want 7d", cfg.Cooldown)
	}
	if cfg.Action.Type != "vm-reset" {
		t.Errorf("default Action.Type = %q, want %q", cfg.Action.Type, "vm-reset")
	}
	if cfg.Action.MaxRetries != 3 {
		t.Errorf("default Action.MaxRetries = %d, want 3", cfg.Action.MaxRetries)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid single action",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action:          ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: false,
		},
		{
			name: "valid with hours",
			cfg: Config{
				CordonThreshold: Duration(30 * time.Minute), ActionThreshold: Duration(1 * time.Hour), Cooldown: Duration(15 * time.Minute),
				Action:          ActionConfig{Type: "noop", Timeout: 5 * time.Minute, MaxRetries: 2},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: false,
		},
		{
			name: "valid multi-step",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action: ActionConfig{MaxRetries: 3, Steps: []ActionStepConfig{
					{Type: "vm-stop", Timeout: 5 * time.Minute},
					{Type: "vm-delete", Timeout: 30 * time.Minute},
				}},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: false,
		},
		{
			name: "cordon >= action",
			cfg: Config{
				CordonThreshold: Duration(60 * 24 * time.Hour), ActionThreshold: Duration(55 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action:          ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "both type and steps",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action: ActionConfig{Type: "vm-reset", MaxRetries: 3, Steps: []ActionStepConfig{
					{Type: "vm-stop", Timeout: 5 * time.Minute},
				}},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "invalid action type",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action:          ActionConfig{Type: "invalid", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "missing crusoeApiSecret",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action:       ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:   GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout: 10 * time.Minute,
				CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "zero cordonThreshold rejected",
			cfg: Config{
				CordonThreshold: 0, ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(7 * 24 * time.Hour),
				Action:          ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "negative cooldown rejected",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: Duration(-1 * time.Hour),
				Action:          ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "zero cooldown allowed",
			cfg: Config{
				CordonThreshold: Duration(55 * 24 * time.Hour), ActionThreshold: Duration(60 * 24 * time.Hour), Cooldown: 0,
				Action:          ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails:      GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		// Days
		{"55d", 55 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"0d", 0, false},
		// Hours
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		// Minutes
		{"30m", 30 * time.Minute, false},
		{"90m", 90 * time.Minute, false},
		// Seconds
		{"1800s", 1800 * time.Second, false},
		{"60s", 60 * time.Second, false},
		// Mixed units
		{"1d12h", 36 * time.Hour, false},
		{"1h30m", 90 * time.Minute, false},
		{"2d3h30m", 2*24*time.Hour + 3*time.Hour + 30*time.Minute, false},
		// Equivalences
		{"1h", 60 * time.Minute, false},  // 1h = 60m
		{"60m", time.Hour, false},        // 60m = 1h
		{"1d", 24 * time.Hour, false},    // 1d = 24h
		// Invalid
		{"invalid", 0, true},
		{"", 0, true},
		{"d", 0, true},
		{"123", 0, true},  // no unit suffix
		// Negative is valid in Go's time.ParseDuration
		{"-5d", -120 * time.Hour, false},
		// Weeks
		{"1w", 7 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"1w3d", 10 * 24 * time.Hour, false},
		{"1w12h", 7*24*time.Hour + 12*time.Hour, false},
		// Equivalences with weeks
		{"1w", 168 * time.Hour, false},   // 1w = 168h
		{"7d", 168 * time.Hour, false},   // 7d = 168h = 1w
		// Weeks edge cases
		{"0w", 0, false},
		{"w", 0, true},  // no number before 'w'
		{"2w1d6h30m", 2*7*24*time.Hour + 24*time.Hour + 6*time.Hour + 30*time.Minute, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDuration(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadConfigMixedUnitsDaysAndHours(t *testing.T) {
	// Test "1d12h" (1 day + 12 hours = 36 hours)
	yamlData := []byte(`
cordonThreshold: 1d12h
actionThreshold: 2d
cooldown: 6h
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.CordonThreshold != Duration(36*time.Hour) {
		t.Errorf("CordonThreshold = %v, want 36h (1d12h)", cfg.CordonThreshold)
	}
	if cfg.ActionThreshold != Duration(48*time.Hour) {
		t.Errorf("ActionThreshold = %v, want 48h (2d)", cfg.ActionThreshold)
	}
	if cfg.Cooldown != Duration(6*time.Hour) {
		t.Errorf("Cooldown = %v, want 6h", cfg.Cooldown)
	}
}

func TestLoadConfigSeconds(t *testing.T) {
	yamlData := []byte(`
cordonThreshold: 60s
actionThreshold: 120s
cooldown: 30s
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.CordonThreshold != Duration(60*time.Second) {
		t.Errorf("CordonThreshold = %v, want 60s", cfg.CordonThreshold)
	}
	if cfg.ActionThreshold != Duration(120*time.Second) {
		t.Errorf("ActionThreshold = %v, want 120s", cfg.ActionThreshold)
	}
	if cfg.Cooldown != Duration(30*time.Second) {
		t.Errorf("Cooldown = %v, want 30s", cfg.Cooldown)
	}
}

func TestLoadConfigInvalidDuration(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{"invalid string", `
cordonThreshold: invalid
actionThreshold: 1h
cooldown: 5m
action: {type: noop, timeout: 5m, maxRetries: 2}
crusoeApiSecret: secret
crusoeProjectId: proj-1
`, true},
		{"empty string", `
cordonThreshold: ""
actionThreshold: 1h
cooldown: 5m
action: {type: noop, timeout: 5m, maxRetries: 2}
crusoeApiSecret: secret
crusoeProjectId: proj-1
`, true},
		{"no unit suffix", `
cordonThreshold: "123"
actionThreshold: 1h
cooldown: 5m
action: {type: noop, timeout: 5m, maxRetries: 2}
crusoeApiSecret: secret
crusoeProjectId: proj-1
`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFromYAML([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadFromYAML() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigWeeks(t *testing.T) {
	// Test weeks in YAML config — end-to-end through LoadFromYAML
	yamlData := []byte(`
cordonThreshold: 1w
actionThreshold: 2w
cooldown: 3d
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.CordonThreshold != Duration(7*24*time.Hour) {
		t.Errorf("CordonThreshold = %v, want 1w (168h)", cfg.CordonThreshold)
	}
	if cfg.ActionThreshold != Duration(14*24*time.Hour) {
		t.Errorf("ActionThreshold = %v, want 2w (336h)", cfg.ActionThreshold)
	}
	if cfg.Cooldown != Duration(3*24*time.Hour) {
		t.Errorf("Cooldown = %v, want 3d (72h)", cfg.Cooldown)
	}
}

func TestLoadConfigWeeksMixedUnits(t *testing.T) {
	// Test weeks mixed with days and hours in YAML config
	yamlData := []byte(`
cordonThreshold: 1w2d6h
actionThreshold: 2w1d
cooldown: 1w12h
action:
  type: noop
  timeout: 5m
  maxRetries: 2
crusoeApiSecret: secret
crusoeProjectId: proj-1
`)

	cfg, err := LoadFromYAML(yamlData)
	if err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	// 1w2d6h = 7d + 2d + 6h = 9d + 6h = 222h
	if cfg.CordonThreshold != Duration(222*time.Hour) {
		t.Errorf("CordonThreshold = %v, want 1w2d6h (222h)", cfg.CordonThreshold)
	}
	// 2w1d = 14d + 1d = 15d = 360h
	if cfg.ActionThreshold != Duration(360*time.Hour) {
		t.Errorf("ActionThreshold = %v, want 2w1d (360h)", cfg.ActionThreshold)
	}
	// 1w12h = 7d + 12h = 168h + 12h = 180h
	if cfg.Cooldown != Duration(180*time.Hour) {
		t.Errorf("Cooldown = %v, want 1w12h (180h)", cfg.Cooldown)
	}
}
