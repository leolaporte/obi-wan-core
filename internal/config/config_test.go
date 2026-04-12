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
claude_binary: /home/leo/.local/bin/claude
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
	require.Equal(t, "/home/leo/.local/bin/claude", cfg.ClaudeBinary)
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
	require.Error(t, err, "should reject config missing claude_binary")
}

func TestLoad_clientFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
claude_binary: /home/leo/.local/bin/claude
state_dir: /tmp/obi-wan-core-test
concurrency: 3
channels:
  telegram:
    enabled: true
    allow_from: ["7902467922"]
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
claude_binary: /home/leo/.local/bin/claude
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
claude_binary: /home/leo/.local/bin/claude
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
claude_binary: /home/leo/.local/bin/claude
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
