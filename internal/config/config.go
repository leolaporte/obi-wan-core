// Package config loads and validates obi-wan-core's YAML config.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root config structure loaded from YAML.
type Config struct {
	APIKeyEnv       string             `yaml:"api_key_env"`
	BaseURL         string             `yaml:"base_url"`
	StateDir        string             `yaml:"state_dir"`
	Concurrency     int                `yaml:"concurrency"`
	Model           string             `yaml:"model"`
	EscalationModel string             `yaml:"escalation_model"`
	TokenBudget     int                `yaml:"token_budget"`
	Fallback        FallbackConfig     `yaml:"fallback"`
	Channels        map[string]Channel `yaml:"channels"`

	// Tool support
	VaultRoot           string `yaml:"vault_root"`
	FastmailTokenEnv    string `yaml:"fastmail_token_env"`
	FastmailUser        string `yaml:"fastmail_user"`
	FastmailPasswordEnv string `yaml:"fastmail_password_env"`
	ClaudeBinary        string `yaml:"claude_binary"`
}

// FallbackTier describes a single fallback provider.
type FallbackTier struct {
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	AuthTokenEnv string `yaml:"auth_token_env,omitempty"`
	Model     string `yaml:"model"`
	Label     string `yaml:"label"`
}

// FallbackConfig holds the fallback provider chain configuration.
type FallbackConfig struct {
	Enabled bool           `yaml:"enabled"`
	Tiers   []FallbackTier `yaml:"tiers"`
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

	// R1 shim
	BootstrapTokenEnv string `yaml:"bootstrap_token_env,omitempty"`
	DeviceStatePath   string `yaml:"device_state_path,omitempty"`
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

	if cfg.APIKeyEnv == "" {
		return nil, fmt.Errorf("config: api_key_env is required")
	}
	if cfg.StateDir == "" {
		return nil, fmt.Errorf("config: state_dir is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}

	if cfg.Concurrency == 0 {
		cfg.Concurrency = 2
	}

	if cfg.Concurrency < 1 {
		return nil, fmt.Errorf("config: concurrency must be >= 1, got %d", cfg.Concurrency)
	}

	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.EscalationModel == "" {
		cfg.EscalationModel = "claude-opus-4-6"
	}
	if cfg.TokenBudget == 0 {
		cfg.TokenBudget = 80000
	}

	for name, ch := range cfg.Channels {
		if ch.OpenAccess && len(ch.AllowFrom) > 0 {
			return nil, fmt.Errorf("config: channel %q has both open_access and allow_from set; remove allow_from", name)
		}
	}

	return &cfg, nil
}
