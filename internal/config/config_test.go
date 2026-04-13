package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad_minimalValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
channels:
  telegram:
    enabled: true
    allow_from:
      - "123456"
  watch:
    enabled: true
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "ANTHROPIC_API_KEY", cfg.APIKeyEnv)
	require.Equal(t, "https://api.anthropic.com", cfg.BaseURL)
	require.Equal(t, "claude-sonnet-4-6", cfg.Model)
	require.Equal(t, 80000, cfg.TokenBudget)
	require.Equal(t, "claude-opus-4-6", cfg.EscalationModel)
	require.Equal(t, "/tmp/obi-wan-core-test", cfg.StateDir)
	require.True(t, cfg.Channels["telegram"].Enabled)
	require.Equal(t, []string{"123456"}, cfg.Channels["telegram"].AllowFrom)
}

func TestLoad_missingFile(t *testing.T) {
	_, err := Load("/does/not/exist.yaml")
	require.Error(t, err)
}

func TestLoad_invalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("this is not: valid: yaml: ["), 0600))

	_, err := Load(path)
	require.Error(t, err)
}

func TestLoad_missingRequiredField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
state_dir: /tmp/test
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	_, err := Load(path)
	require.Error(t, err, "should reject config missing api_key_env")
}

func TestLoad_clientFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
concurrency: 3
channels:
  telegram:
    enabled: true
    allow_from: ["123456789"]
    system_prompt_file: /home/leo/.claude/channels/telegram/system-prompt.md
    bot_token_env: TELEGRAM_BOT_TOKEN
  watch:
    enabled: true
    open_access: true
    webhook_port: 8199
    webhook_key_env: WEBHOOK_KEY
    watch_chat_id_env: WATCH_CHAT_ID
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "ANTHROPIC_API_KEY", cfg.APIKeyEnv)
	require.Equal(t, 3, cfg.Concurrency)
	require.Equal(t, "/home/leo/.claude/channels/telegram/system-prompt.md", cfg.Channels["telegram"].SystemPromptFile)
	require.Equal(t, "TELEGRAM_BOT_TOKEN", cfg.Channels["telegram"].BotTokenEnv)
	require.True(t, cfg.Channels["watch"].OpenAccess)
	require.Equal(t, 8199, cfg.Channels["watch"].WebhookPort)
	require.Equal(t, "WEBHOOK_KEY", cfg.Channels["watch"].WebhookKeyEnv)
	require.Equal(t, "WATCH_CHAT_ID", cfg.Channels["watch"].WatchChatIDEnv)
}

func TestLoad_concurrencyDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
channels:
  telegram:
    enabled: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 2, cfg.Concurrency, "unset concurrency defaults to 2")
}

func TestLoad_openAccessWithAllowFromRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
channels:
  watch:
    enabled: true
    open_access: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	_, err := Load(path)
	require.Error(t, err, "should reject channel with both open_access and allow_from")
	require.Contains(t, err.Error(), "open_access")
}

func TestLoad_negativeConcurrencyRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
concurrency: -1
channels:
  telegram:
    enabled: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	_, err := Load(path)
	require.Error(t, err, "negative concurrency should be rejected")
	require.Contains(t, err.Error(), "concurrency")
}

func TestLoad_fallbackTiers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
model: opus
fallback:
  enabled: true
  tiers:
    - base_url: https://api.z.ai/api/anthropic
      api_key_env: ZAI_API_KEY
      model: glm-5.1
      label: GLM
    - base_url: http://localhost:11434
      auth_token_env: OLLAMA_AUTH_TOKEN
      model: qwen3.5:35b
      label: Ollama
channels:
  telegram:
    enabled: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "opus", cfg.Model)
	require.True(t, cfg.Fallback.Enabled)
	require.Len(t, cfg.Fallback.Tiers, 2)
	require.Equal(t, "https://api.z.ai/api/anthropic", cfg.Fallback.Tiers[0].BaseURL)
	require.Equal(t, "ZAI_API_KEY", cfg.Fallback.Tiers[0].APIKeyEnv)
	require.Equal(t, "glm-5.1", cfg.Fallback.Tiers[0].Model)
	require.Equal(t, "GLM", cfg.Fallback.Tiers[0].Label)
	require.Equal(t, "http://localhost:11434", cfg.Fallback.Tiers[1].BaseURL)
	require.Equal(t, "OLLAMA_AUTH_TOKEN", cfg.Fallback.Tiers[1].AuthTokenEnv)
	require.Equal(t, "qwen3.5:35b", cfg.Fallback.Tiers[1].Model)
	require.Equal(t, "Ollama", cfg.Fallback.Tiers[1].Label)
}

func TestLoad_modelDefaultsToSonnet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
channels:
  telegram:
    enabled: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-6", cfg.Model, "unset model defaults to claude-sonnet-4-6")
}

func TestLoad_toolConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
vault_root: ~/Obsidian/lgl
fastmail_token_env: FASTMAIL_API_TOKEN
fastmail_user: leo@fastmail.com
fastmail_password_env: FASTMAIL_PASSWORD
claude_binary: /home/leo/.local/bin/claude
channels:
  telegram:
    enabled: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "~/Obsidian/lgl", cfg.VaultRoot)
	require.Equal(t, "FASTMAIL_API_TOKEN", cfg.FastmailTokenEnv)
	require.Equal(t, "leo@fastmail.com", cfg.FastmailUser)
	require.Equal(t, "FASTMAIL_PASSWORD", cfg.FastmailPasswordEnv)
	require.Equal(t, "/home/leo/.local/bin/claude", cfg.ClaudeBinary)
}

func TestLoad_R1Channel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "api_key_env: ANTHROPIC_API_KEY\n" +
		"state_dir: " + dir + "\n" +
		"channels:\n" +
		"  r1:\n" +
		"    enabled: true\n" +
		"    webhook_port: 18789\n" +
		"    bootstrap_token_env: R1_BOOTSTRAP_TOKEN\n" +
		"    device_state_path: " + dir + "/r1-devices.json\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	ch, ok := cfg.Channels["r1"]
	require.True(t, ok, "r1 channel must exist")
	require.True(t, ch.Enabled)
	require.Equal(t, 18789, ch.WebhookPort)
	require.Equal(t, "R1_BOOTSTRAP_TOKEN", ch.BootstrapTokenEnv)
	require.Equal(t, dir+"/r1-devices.json", ch.DeviceStatePath)
}
