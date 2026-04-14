package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildDispatcherWithConfig verifies the wiring in buildDispatcherWithConfig:
// a minimal valid config produces a non-nil dispatcher and a cfg with the
// documented Concurrency=2 default when concurrency is omitted.
func TestBuildDispatcherWithConfig(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	cfgPath := filepath.Join(dir, "config.yaml")

	yaml := "api_key_env: ANTHROPIC_API_KEY\n" +
		"state_dir: " + stateDir + "\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	d, cfg, err := buildDispatcherWithConfig(cfgPath)
	if err != nil {
		t.Fatalf("buildDispatcherWithConfig: %v", err)
	}
	if d == nil {
		t.Fatal("dispatcher is nil")
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
	if cfg.Concurrency != 2 {
		t.Errorf("Concurrency = %d, want 2 (default)", cfg.Concurrency)
	}
	if cfg.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("APIKeyEnv = %q, want ANTHROPIC_API_KEY", cfg.APIKeyEnv)
	}
	if cfg.StateDir != stateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, stateDir)
	}
}
