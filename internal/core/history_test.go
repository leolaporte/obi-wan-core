package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistory_LoadMissing_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(filepath.Join(dir, "history.json"), 10000)

	msgs, err := h.Load()
	require.NoError(t, err)
	require.Empty(t, msgs)
}

func TestHistory_SaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(filepath.Join(dir, "history.json"), 10000)

	input := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	require.NoError(t, h.Save(input))

	got, err := h.Load()
	require.NoError(t, err)
	require.Equal(t, input, got)
}

func TestHistory_Append_AddsUserAndAssistantPair(t *testing.T) {
	h := NewHistory("/dev/null", 10000)

	existing := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "first reply"},
	}

	result := h.Append(existing, "second", "second reply")
	require.Len(t, result, 4)
	require.Equal(t, "second", result[2].Content)
	require.Equal(t, "user", result[2].Role)
	require.Equal(t, "second reply", result[3].Content)
	require.Equal(t, "assistant", result[3].Role)
}

func TestHistory_Prune_DropsOldestPairsWhenOverBudget(t *testing.T) {
	// budget of 5 tokens; each message with 20 chars = 5 tokens
	// two pairs = 4 messages = 20 tokens total, over budget of 5
	h := NewHistory("/dev/null", 5)

	msgs := []Message{
		{Role: "user", Content: "aaaabbbbcccc"}, // 3 tokens
		{Role: "assistant", Content: "aaaabbbbcccc"}, // 3 tokens
		{Role: "user", Content: "dddd"},          // 1 token
		{Role: "assistant", Content: "dddd"},      // 1 token
	}

	pruned := h.Prune(msgs)
	// Should have dropped the first pair, keeping only the last pair (2 tokens <= 5)
	require.Len(t, pruned, 2)
	require.Equal(t, "dddd", pruned[0].Content)
	require.Equal(t, "dddd", pruned[1].Content)
}

func TestHistory_Prune_EmptyOnAllOverBudget(t *testing.T) {
	// budget of 1 token; even one message is over budget
	h := NewHistory("/dev/null", 1)

	msgs := []Message{
		{Role: "user", Content: "aaaabbbbcccc"},      // 3 tokens
		{Role: "assistant", Content: "aaaabbbbcccc"}, // 3 tokens
	}

	pruned := h.Prune(msgs)
	require.Empty(t, pruned)
}

func TestHistory_Prune_NoPruneWhenUnderBudget(t *testing.T) {
	h := NewHistory("/dev/null", 10000)

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	pruned := h.Prune(msgs)
	require.Equal(t, msgs, pruned)
}

func TestHistory_LoadCorrupt_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	require.NoError(t, os.WriteFile(path, []byte("not valid json{{{{"), 0600))

	h := NewHistory(path, 10000)
	msgs, err := h.Load()
	require.NoError(t, err)
	require.Empty(t, msgs)
}
