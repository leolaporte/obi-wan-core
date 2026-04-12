package core

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockClaudeScript creates a temporary bash script at <dir>/claude that
// emits fixed stdout/stderr and returns a fixed exit code.
func mockClaudeScript(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/bash\n" +
		"cat <<'STDOUT'\n" + stdout + "\nSTDOUT\n" +
		"cat >&2 <<'STDERR'\n" + stderr + "\nSTDERR\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0700))
	return path
}

func TestClaudeRunner_successJSON(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":"Hello from mock"}`, "", 0)

	runner := NewClaudeRunner(bin, "sonnet")
	result, err := runner.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc123",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Equal(t, "Hello from mock", result.Text)
	require.False(t, result.SessionError)
}

func TestClaudeRunner_emptyOutputFallback(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":""}`, "", 0)

	runner := NewClaudeRunner(bin, "sonnet")
	result, err := runner.Run(context.Background(), RunArgs{
		Message: "hello", SessionID: "abc", IsNewSession: true,
	})
	require.NoError(t, err)
	require.Equal(t, "(no output)", result.Text)
}

func TestClaudeRunner_sessionErrorDetected(t *testing.T) {
	bin := mockClaudeScript(t, "", "session not found: abc123", 1)

	runner := NewClaudeRunner(bin, "sonnet")
	result, err := runner.Run(context.Background(), RunArgs{
		Message: "hello", SessionID: "abc", IsNewSession: false,
	})
	require.NoError(t, err, "session error is a result, not an error")
	require.True(t, result.SessionError)
}

func TestClaudeRunner_nonZeroExitWithoutSessionError(t *testing.T) {
	bin := mockClaudeScript(t, "", "some other failure", 2)

	runner := NewClaudeRunner(bin, "sonnet")
	result, err := runner.Run(context.Background(), RunArgs{
		Message: "hello", SessionID: "abc", IsNewSession: false,
	})
	require.NoError(t, err, "wrapper returns an error result, not a Go error")
	require.False(t, result.SessionError)
	require.Contains(t, result.Text, "Error")
}
