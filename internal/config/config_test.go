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
    webhook_key: test-key
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "/home/leo/.local/bin/claude", cfg.ClaudeBinary)
	require.Equal(t, "/tmp/obi-wan-core-test", cfg.StateDir)
	require.True(t, cfg.Channels["telegram"].Enabled)
	require.Equal(t, []string{"123456"}, cfg.Channels["telegram"].AllowFrom)
	require.Equal(t, "test-key", cfg.Channels["watch"].WebhookKey)
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
