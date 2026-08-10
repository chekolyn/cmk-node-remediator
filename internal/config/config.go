package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// ActionStepConfig holds a single step in a multi-step remediation action.
type ActionStepConfig struct {
	Type    string            `yaml:"type"`
	Timeout time.Duration     `yaml:"timeout"`
	Wait    time.Duration     `yaml:"wait,omitempty"`
	Params  map[string]string `yaml:"params,omitempty"`
}

// ActionConfig holds the configurable remediation action settings.
// Either Type (single step) or Steps (multi-step sequence) must be set, not both.
type ActionConfig struct {
	Type       string             `yaml:"type,omitempty"`
	Timeout    time.Duration      `yaml:"timeout,omitempty"`
	MaxRetries int                `yaml:"maxRetries"`
	Params     map[string]string  `yaml:"params,omitempty"`
	Steps      []ActionStepConfig `yaml:"steps,omitempty"`
}

// GuardrailsConfig holds concurrency limit settings.
type GuardrailsConfig struct {
	GlobalMaxCordoned         int  `yaml:"globalMaxCordoned"`
	GlobalMaxCordonedPercent  *int `yaml:"globalMaxCordonedPercent,omitempty"`
	PerPoolMaxCordoned        int  `yaml:"perPoolMaxCordoned"`
	PerPoolMaxCordonedPercent *int `yaml:"perPoolMaxCordonedPercent,omitempty"`
}

// NodeSelectorConfig holds label selector for target node discovery.
type NodeSelectorConfig struct {
	MatchLabels map[string]string `yaml:"matchLabels,omitempty"`
}

// Config holds all configuration for the node remediation manager.
type Config struct {
	Schedule                string             `yaml:"schedule"`
	VMInstanceTypeFilter    string             `yaml:"vmInstanceTypeFilter,omitempty"`
	CordonDays              int                `yaml:"cordonDays"`
	ActionDays              int                `yaml:"actionDays"`
	CooldownDays            int                `yaml:"cooldownDays"`
	Action                  ActionConfig       `yaml:"action"`
	Guardrails              GuardrailsConfig   `yaml:"guardrails"`
	DrainTimeout            time.Duration      `yaml:"drainTimeout"`
	ForceDrainAfterTimeout  bool               `yaml:"forceDrainAfterTimeout"`
	CrusoeAPISecret         string             `yaml:"crusoeApiSecret"`
	CrusoeProjectID         string             `yaml:"crusoeProjectId"`
	DryRun                  bool               `yaml:"dryRun"`
	SlackWebhookURL         string             `yaml:"slackWebhookUrl,omitempty"`
	NodeSelector            NodeSelectorConfig `yaml:"nodeSelector,omitempty"`
}

// ValidStepTypes lists the supported remediation step types.
var ValidStepTypes = map[string]bool{
	"vm-reset":  true,
	"vm-stop":   true,
	"vm-start":  true,
	"vm-delete": true,
	"noop":      true,
}

func LoadFromYAML(data []byte) (*Config, error) {
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config YAML: %w", err)
	}
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Schedule:             "0 0 * * *",
		VMInstanceTypeFilter: "b200.*",
		CordonDays:           55,
		ActionDays:           60,
		CooldownDays:         7,
		Action: ActionConfig{
			Type:       "vm-reset",
			Timeout:    30 * time.Minute,
			MaxRetries: 3,
		},
		Guardrails: GuardrailsConfig{
			GlobalMaxCordoned:  5,
			PerPoolMaxCordoned: 2,
		},
		DrainTimeout:           10 * time.Minute,
		ForceDrainAfterTimeout: true,
		DryRun:                 false,
	}
}

func (c *Config) Validate() error {
	if c.CordonDays >= c.ActionDays {
		return fmt.Errorf("cordonDays (%d) must be less than actionDays (%d)", c.CordonDays, c.ActionDays)
	}
	if c.CrusoeAPISecret == "" {
		return fmt.Errorf("crusoeApiSecret must be set")
	}
	if c.CrusoeProjectID == "" {
		return fmt.Errorf("crusoeProjectId must be set")
	}

	hasType := c.Action.Type != ""
	hasSteps := len(c.Action.Steps) > 0
	if hasType && hasSteps {
		return fmt.Errorf("action.type and action.steps are mutually exclusive")
	}
	if !hasType && !hasSteps {
		return fmt.Errorf("either action.type or action.steps must be set")
	}
	if hasType && !ValidStepTypes[c.Action.Type] {
		return fmt.Errorf("invalid action type %q", c.Action.Type)
	}
	if hasSteps {
		for i, s := range c.Action.Steps {
			if !ValidStepTypes[s.Type] {
				return fmt.Errorf("invalid step type %q at index %d", s.Type, i)
			}
		}
	}
	if c.Action.MaxRetries <= 0 {
		return fmt.Errorf("action.maxRetries must be positive")
	}
	if c.Guardrails.GlobalMaxCordoned == 0 && c.Guardrails.GlobalMaxCordonedPercent == nil {
		return fmt.Errorf("either globalMaxCordoned or globalMaxCordonedPercent must be set")
	}
	if c.Guardrails.PerPoolMaxCordoned == 0 && c.Guardrails.PerPoolMaxCordonedPercent == nil {
		return fmt.Errorf("either perPoolMaxCordoned or perPoolMaxCordonedPercent must be set")
	}
	return nil
}
