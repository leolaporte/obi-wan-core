package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFallbackRunner_primarySuccess(t *testing.T) {
	primaryBin := mockClaudeScript(t, `{"result":"primary reply"}`, "", 0)
	fallbackBin := mockClaudeScript(t, `{"result":"fallback reply"}`, "", 0)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	fallback := NewClaudeRunner(fallbackBin, "glm-5.1")
	fr := NewFallbackRunner(primary, fallback, "GLM")

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Equal(t, "primary reply", result.Text, "should return primary result without prefix")
	require.False(t, result.SessionError)
}

func TestFallbackRunner_primaryFailsFallsBack(t *testing.T) {
	primaryBin := mockClaudeScript(t, "", "401 unauthorized", 1)
	fallbackBin := mockClaudeScript(t, `{"result":"fallback reply"}`, "", 0)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	fallback := NewClaudeRunner(fallbackBin, "glm-5.1")
	fr := NewFallbackRunner(primary, fallback, "GLM")

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Equal(t, "[GLM] fallback reply", result.Text, "should prefix fallback result")
	require.False(t, result.SessionError)
}

func TestFallbackRunner_bothFail(t *testing.T) {
	primaryBin := mockClaudeScript(t, "", "primary error", 1)
	fallbackBin := mockClaudeScript(t, "", "fallback error", 1)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	fallback := NewClaudeRunner(fallbackBin, "glm-5.1")
	fr := NewFallbackRunner(primary, fallback, "GLM")

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Contains(t, result.Text, "[GLM]", "should prefix even on fallback failure")
	require.Contains(t, result.Text, "Error running claude", "should surface fallback error")
}

func TestFallbackRunner_sessionErrorDoesNotTriggerFallback(t *testing.T) {
	primaryBin := mockClaudeScript(t, "", "session not found", 1)
	fallbackBin := mockClaudeScript(t, `{"result":"should not be used"}`, "", 0)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	fallback := NewClaudeRunner(fallbackBin, "glm-5.1")
	fr := NewFallbackRunner(primary, fallback, "GLM")

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: false,
	})
	require.NoError(t, err)
	require.True(t, result.SessionError, "session errors pass through without fallback")
}

func TestFallbackRunner_nilFallbackPassthrough(t *testing.T) {
	primaryBin := mockClaudeScript(t, "", "some error", 1)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	fr := NewFallbackRunner(primary, nil, "GLM")

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Contains(t, result.Text, "Error running claude", "should return primary error when no fallback")
	require.NotContains(t, result.Text, "[GLM]", "should not prefix when no fallback attempted")
}
