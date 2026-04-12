package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunk_shortPassesThrough(t *testing.T) {
	out := Chunk("hello world")
	require.Equal(t, []string{"hello world"}, out)
}

func TestChunk_emptyReturnsEmpty(t *testing.T) {
	out := Chunk("")
	require.Empty(t, out)
}

func TestChunk_splitsOnParagraphBoundary(t *testing.T) {
	// Build a string where a paragraph break sits comfortably before the limit.
	p1 := strings.Repeat("a", 3000)
	p2 := strings.Repeat("b", 2000)
	out := Chunk(p1 + "\n\n" + p2)
	require.Len(t, out, 2)
	require.Equal(t, p1, out[0])
	require.Equal(t, p2, out[1])
}

func TestChunk_splitsOnNewlineWhenNoParagraphNearBoundary(t *testing.T) {
	// Long single paragraph with mid-string newlines — should pick the last
	// \n inside the limit, not the start.
	big := strings.Repeat("word ", 1000) // 5000 chars
	// Insert a newline 4000 chars in.
	withNL := big[:4000] + "\n" + big[4000:]
	out := Chunk(withNL)
	require.GreaterOrEqual(t, len(out), 2)
	require.LessOrEqual(t, len(out[0]), 4096)
}

func TestChunk_hardSplitsWhenNoBoundaryInRange(t *testing.T) {
	big := strings.Repeat("x", 10000) // no whitespace at all
	out := Chunk(big)
	require.GreaterOrEqual(t, len(out), 3)
	for _, c := range out {
		require.LessOrEqual(t, len(c), 4096)
	}
	require.Equal(t, big, strings.Join(out, ""))
}
