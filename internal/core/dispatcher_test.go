package core

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/memory"
	"github.com/stretchr/testify/require"
)

func newTestDispatcher(t *testing.T, mockBin string) (*Dispatcher, string) {
	t.Helper()
	stateDir := t.TempDir()
	memDir := t.TempDir()
	cfg := &config.Config{
		ClaudeBinary: mockBin,
		StateDir:     stateDir,
		Concurrency:  2, // must be >= 1; matches config.Load default
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	sessions, err := NewSessionStore(stateDir)
	require.NoError(t, err)
	d := NewDispatcher(
		cfg,
		NewAccess(cfg),
		sessions,
		memory.NewLoader(memDir),
		NewClaudeRunner(mockBin, "sonnet"),
	)
	return d, memDir
}

func TestDispatcher_allowedTurnReturnsReply(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":"hi alice"}`, "", 0)
	d, _ := newTestDispatcher(t, bin)

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel:    "telegram",
		UserID:     "alice",
		Message:    "hello",
		ReceivedAt: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, "hi alice", reply.Text)
}

func TestDispatcher_deniedUserReturnsError(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":"should not run"}`, "", 0)
	d, _ := newTestDispatcher(t, bin)

	_, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "mallory", Message: "hi",
	})
	require.ErrorIs(t, err, ErrAccessDenied)
}

func TestDispatcher_sessionErrorTriggersRotation(t *testing.T) {
	bin := mockClaudeScript(t, "", "session not found", 1)
	d, _ := newTestDispatcher(t, bin)

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi",
	})
	require.NoError(t, err)
	require.NotEmpty(t, reply.Text)
}

func TestDispatcher_memoryInjectedIntoSystemPrompt(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":"ok"}`, "", 0)
	d, memDir := newTestDispatcher(t, bin)

	chanDir := filepath.Join(memDir, "telegram")
	require.NoError(t, os.MkdirAll(chanDir, 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(chanDir, "memory.md"),
		[]byte("Leo likes tea."),
		0600,
	))

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi",
	})
	require.NoError(t, err)
	require.Equal(t, "ok", reply.Text)
}

func TestDispatcher_concurrencyCapSerializes(t *testing.T) {
	// Mock claude that sleeps for 150ms so we can observe serialization.
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/bash\nsleep 0.15\ncat <<'STDOUT'\n{\"result\":\"ok\"}\nSTDOUT\nexit 0\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0700))

	stateDir := t.TempDir()
	cfg := &config.Config{
		ClaudeBinary: bin,
		StateDir:     stateDir,
		Concurrency:  1, // force strict serialization
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	sessions, err := NewSessionStore(stateDir)
	require.NoError(t, err)
	d := NewDispatcher(cfg, NewAccess(cfg), sessions, memory.NewLoader(t.TempDir()), NewClaudeRunner(bin, "sonnet"))

	// Fire three concurrent dispatches. With concurrency=1 and each taking
	// ~150ms, three serialized runs should take ~450ms; we assert ≥400ms
	// to leave 50ms headroom for CI scheduling jitter.
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Dispatch(context.Background(), Turn{
				Channel: "telegram", UserID: "alice", Message: "hi",
			})
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, 400*time.Millisecond, "concurrency=1 should serialize calls")
}
