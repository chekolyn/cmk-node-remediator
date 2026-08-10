package config

import (
	"testing"
	"time"
)

func TestLoadConfigFromYAML(t *testing.T) {
	yamlData := []byte(`schedule: "0 0 * * *"
vmInstanceTypeFilter: "b200.*"
cordonDays: 55
actionDays: 60
cooldownDays: 7
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

	if cfg.CordonDays != 55 {
		t.Errorf("CordonDays = %d, want 55", cfg.CordonDays)
	}
	if cfg.ActionDays != 60 {
		t.Errorf("ActionDays = %d, want 60", cfg.ActionDays)
	}
	if cfg.CooldownDays != 7 {
		t.Errorf("CooldownDays = %d, want 7", cfg.CooldownDays)
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

func TestLoadConfigMultiStepAction(t *testing.T) {
	yamlData := []byte(`cordonDays: 55
actionDays: 60
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
	if cfg.CordonDays != 55 {
		t.Errorf("default CordonDays = %d, want 55", cfg.CordonDays)
	}
	if cfg.ActionDays != 60 {
		t.Errorf("default ActionDays = %d, want 60", cfg.ActionDays)
	}
	if cfg.CooldownDays != 7 {
		t.Errorf("default CooldownDays = %d, want 7", cfg.CooldownDays)
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
				CordonDays: 55, ActionDays: 60, CooldownDays: 7,
				Action: ActionConfig{
					Type:       "vm-reset",
					Timeout:    30 * time.Minute,
					MaxRetries: 3,
				},
				Guardrails: GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: false,
		},
		{
			name: "valid multi-step",
			cfg: Config{
				CordonDays: 55, ActionDays: 60, CooldownDays: 7,
				Action: ActionConfig{MaxRetries: 3, Steps: []ActionStepConfig{
					{Type: "vm-stop", Timeout: 5 * time.Minute},
					{Type: "vm-delete", Timeout: 30 * time.Minute},
				}},
				Guardrails: GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: false,
		},
		{
			name: "cordonDays >= actionDays",
			cfg: Config{
				CordonDays: 60, ActionDays: 55, CooldownDays: 7,
				Action: ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails: GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "both type and steps",
			cfg: Config{
				CordonDays: 55, ActionDays: 60, CooldownDays: 7,
				Action: ActionConfig{Type: "vm-reset", MaxRetries: 3, Steps: []ActionStepConfig{
					{Type: "vm-stop", Timeout: 5 * time.Minute},
				}},
				Guardrails: GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "invalid action type",
			cfg: Config{
				CordonDays: 55, ActionDays: 60, CooldownDays: 7,
				Action: ActionConfig{Type: "invalid", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails: GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeAPISecret: "secret", CrusoeProjectID: "proj-1",
			},
			wantErr: true,
		},
		{
			name: "missing crusoeApiSecret",
			cfg: Config{
				CordonDays: 55, ActionDays: 60, CooldownDays: 7,
				Action: ActionConfig{Type: "vm-reset", Timeout: 30 * time.Minute, MaxRetries: 3},
				Guardrails: GuardrailsConfig{GlobalMaxCordoned: 5, PerPoolMaxCordoned: 2},
				DrainTimeout:    10 * time.Minute,
				CrusoeProjectID: "proj-1",
			},
			wantErr: true,
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