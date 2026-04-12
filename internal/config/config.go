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
	Concurrency  int                `yaml:"concurrency"`
	Channels     map[string]Channel `yaml:"channels"`
}

// Channel is the per-channel configuration.
type Channel struct {
	Enabled          bool     `yaml:"enabled"`
	AllowFrom        []string `yaml:"allow_from,omitempty"`
	OpenAccess       bool     `yaml:"open_access,omitempty"`
	SystemPromptFile string   `yaml:"system_prompt_file,omitempty"`

	// Telegram client
	BotTokenEnv string `yaml:"bot_token_env,omitempty"`

	// Watch webhook client
	WebhookPort    int    `yaml:"webhook_port,omitempty"`
	WebhookKeyEnv  string `yaml:"webhook_key_env,omitempty"`
	WatchChatIDEnv string `yaml:"watch_chat_id_env,omitempty"`
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

	if cfg.Concurrency == 0 {
		cfg.Concurrency = 2
	}

	if cfg.Concurrency < 1 {
		return nil, fmt.Errorf("config: concurrency must be >= 1, got %d", cfg.Concurrency)
	}

	for name, ch := range cfg.Channels {
		if ch.OpenAccess && len(ch.AllowFrom) > 0 {
			return nil, fmt.Errorf("config: channel %q has both open_access and allow_from set; remove allow_from", name)
		}
	}

	return &cfg, nil
}
