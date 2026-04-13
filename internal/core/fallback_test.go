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
	fr := NewFallbackRunner(primary, []fallbackTier{
		{runner: fallback, label: "GLM"},
	})

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
	fr := NewFallbackRunner(primary, []fallbackTier{
		{runner: fallback, label: "GLM"},
	})

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
	fr := NewFallbackRunner(primary, []fallbackTier{
		{runner: fallback, label: "GLM"},
	})

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
	fr := NewFallbackRunner(primary, []fallbackTier{
		{runner: fallback, label: "GLM"},
	})

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
	fr := NewFallbackRunner(primary, nil)

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Contains(t, result.Text, "Error running claude", "should return primary error when no fallback")
	require.NotContains(t, result.Text, "[GLM]", "should not prefix when no fallback attempted")
}

func TestFallbackRunner_threeTierChain(t *testing.T) {
	// Primary fails, first fallback (GLM) fails, second fallback (Ollama) succeeds.
	primaryBin := mockClaudeScript(t, "", "primary error", 1)
	glmBin := mockClaudeScript(t, "", "glm error", 1)
	ollamaBin := mockClaudeScript(t, `{"result":"local reply"}`, "", 0)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	glmRunner := NewClaudeRunner(glmBin, "glm-5.1")
	ollamaRunner := NewClaudeRunner(ollamaBin, "qwen3.5:35b")
	fr := NewFallbackRunner(primary, []fallbackTier{
		{runner: glmRunner, label: "GLM"},
		{runner: ollamaRunner, label: "Ollama"},
	})

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Equal(t, "[Ollama] local reply", result.Text, "should use second fallback and prefix")
}

func TestFallbackRunner_allTiersFail(t *testing.T) {
	primaryBin := mockClaudeScript(t, "", "primary error", 1)
	glmBin := mockClaudeScript(t, "", "glm error", 1)
	ollamaBin := mockClaudeScript(t, "", "ollama error", 1)

	primary := NewClaudeRunner(primaryBin, "sonnet")
	glmRunner := NewClaudeRunner(glmBin, "glm-5.1")
	ollamaRunner := NewClaudeRunner(ollamaBin, "qwen3.5:35b")
	fr := NewFallbackRunner(primary, []fallbackTier{
		{runner: glmRunner, label: "GLM"},
		{runner: ollamaRunner, label: "Ollama"},
	})

	result, err := fr.Run(context.Background(), RunArgs{
		Message:      "hello",
		SessionID:    "abc",
		IsNewSession: true,
	})
	require.NoError(t, err)
	require.Contains(t, result.Text, "[Ollama]", "should prefix with last tier that was tried")
	require.Contains(t, result.Text, "Error running claude", "should surface last fallback error")
}
