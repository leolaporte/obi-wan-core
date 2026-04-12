package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionStore_loadOrCreate_fresh(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	require.NoError(t, err)

	id1, fresh1 := store.LoadOrCreate("telegram")
	require.NotEmpty(t, id1)
	require.True(t, fresh1, "brand-new channel should be fresh")

	// Second call returns the same ID and is no longer fresh.
	id2, fresh2 := store.LoadOrCreate("telegram")
	require.Equal(t, id1, id2)
	require.False(t, fresh2)
}

func TestSessionStore_perChannelIsolation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	require.NoError(t, err)

	tg, _ := store.LoadOrCreate("telegram")
	r1, _ := store.LoadOrCreate("r1")
	require.NotEqual(t, tg, r1, "each channel gets its own session ID")
}

func TestSessionStore_persistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store1, err := NewSessionStore(dir)
	require.NoError(t, err)
	original, _ := store1.LoadOrCreate("telegram")

	// Simulate process restart: new store, same dir.
	store2, err := NewSessionStore(dir)
	require.NoError(t, err)
	persisted, fresh := store2.LoadOrCreate("telegram")
	require.Equal(t, original, persisted)
	require.False(t, fresh, "persisted session is not fresh")
}

func TestSessionStore_rotate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	require.NoError(t, err)

	id1, _ := store.LoadOrCreate("telegram")
	id2 := store.Rotate("telegram")
	require.NotEqual(t, id1, id2)

	// After rotate, loading returns the new ID and it's fresh.
	id3, fresh := store.LoadOrCreate("telegram")
	require.Equal(t, id2, id3)
	require.True(t, fresh, "freshly rotated session is fresh")
}

func TestSessionStore_sessionFileExists(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	require.NoError(t, err)
	store.LoadOrCreate("telegram")

	path := filepath.Join(dir, "sessions", "telegram.session")
	_, err = os.Stat(path)
	require.NoError(t, err, "session file should be created on disk")
}
