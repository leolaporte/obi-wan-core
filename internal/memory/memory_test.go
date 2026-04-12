package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoader_readsExistingFile(t *testing.T) {
	dir := t.TempDir()
	chanDir := filepath.Join(dir, "telegram")
	require.NoError(t, os.MkdirAll(chanDir, 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(chanDir, "memory.md"),
		[]byte("# Telegram Memory\nLeo likes tea."),
		0600,
	))

	l := NewLoader(dir)
	content, err := l.Load("telegram")
	require.NoError(t, err)
	require.Contains(t, content, "Leo likes tea.")
}

func TestLoader_missingFileReturnsEmptyString(t *testing.T) {
	dir := t.TempDir()
	l := NewLoader(dir)
	content, err := l.Load("nonexistent")
	require.NoError(t, err, "missing memory file is not an error")
	require.Equal(t, "", content)
}

func TestLoader_maxSizeEnforced(t *testing.T) {
	dir := t.TempDir()
	chanDir := filepath.Join(dir, "telegram")
	require.NoError(t, os.MkdirAll(chanDir, 0700))
	big := make([]byte, 65*1024)
	for i := range big {
		big[i] = 'x'
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(chanDir, "memory.md"),
		big,
		0600,
	))

	l := NewLoader(dir)
	_, err := l.Load("telegram")
	require.Error(t, err, "oversized memory file should error")
}
