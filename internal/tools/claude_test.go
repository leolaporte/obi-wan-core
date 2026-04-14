package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockClaudeBinary creates a bash script that writes its arguments to a marker file.
func mockClaudeBinary(t *testing.T, markerFile string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	content := "#!/bin/bash\necho \"$@\" > " + markerFile + "\n"
	require.NoError(t, os.WriteFile(script, []byte(content), 0o755))
	return script
}

func TestSpawnClaude_FiresProcess(t *testing.T) {
	markerFile := filepath.Join(t.TempDir(), "marker.txt")
	binary := mockClaudeBinary(t, markerFile)

	handler := SpawnClaudeCodeHandler(binary)
	input, _ := json.Marshal(spawnClaudeInput{
		Task:  "research quantum computing",
		Skill: "research",
	})

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, result, "spawned")

	// Wait for background process to complete
	time.Sleep(500 * time.Millisecond)

	data, err := os.ReadFile(markerFile)
	require.NoError(t, err, "marker file should exist after process completes")

	contents := string(data)
	require.Contains(t, contents, "research")
	require.Contains(t, contents, "quantum computing")
}

func TestSpawnClaude_NoSkill(t *testing.T) {
	markerFile := filepath.Join(t.TempDir(), "marker.txt")
	binary := mockClaudeBinary(t, markerFile)

	handler := SpawnClaudeCodeHandler(binary)
	input, _ := json.Marshal(spawnClaudeInput{
		Task: "summarize the meeting notes",
	})

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, result, "spawned")

	// Wait for background process to complete
	time.Sleep(500 * time.Millisecond)

	data, err := os.ReadFile(markerFile)
	require.NoError(t, err, "marker file should exist after process completes")

	contents := string(data)
	require.Contains(t, contents, "summarize the meeting notes")
	// Without a skill, the prompt should not start with "/"
	require.NotContains(t, contents, "/summarize")
}
