// Package config loads and validates obi-wan-core's YAML config.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root config structure loaded from YAML.
type Config struct {
	ClaudeBinary string             `yaml:"claude_binary"`
	StateDir     string             `yaml:"state_dir"`
	Channels     map[string]Channel `yaml:"channels"`
}

// Channel is the per-channel configuration.
type Channel struct {
	Enabled    bool     `yaml:"enabled"`
	AllowFrom  []string `yaml:"allow_from"`
	WebhookKey string   `yaml:"webhook_key,omitempty"`
}

// Load reads a YAML config file and validates required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if cfg.ClaudeBinary == "" {
		return nil, fmt.Errorf("config: claude_binary is required")
	}
	if cfg.StateDir == "" {
		return nil, fmt.Errorf("config: state_dir is required")
	}

	return &cfg, nil
}
