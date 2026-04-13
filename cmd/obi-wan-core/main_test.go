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

	yaml := "claude_binary: /bin/true\n" +
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
	if cfg.ClaudeBinary != "/bin/true" {
		t.Errorf("ClaudeBinary = %q, want /bin/true", cfg.ClaudeBinary)
	}
	if cfg.StateDir != stateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, stateDir)
	}
}
