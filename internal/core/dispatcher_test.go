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
		APIKeyEnv: "ANTHROPIC_API_KEY",
		StateDir:     stateDir,
		Concurrency:  2, // must be >= 1; matches config.Load default
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	sessions, err := NewSessionStore(stateDir)
	require.NoError(t, err)
	runner := NewClaudeRunner(mockBin, "sonnet")
	fb := NewFallbackRunner(runner, nil)
	d := NewDispatcher(
		cfg,
		NewAccess(cfg),
		sessions,
		memory.NewLoader(memDir),
		fb,
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

func TestDispatcher_systemPromptFileCombinesWithMemory(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":"ok"}`, "", 0)

	stateDir := t.TempDir()
	memDir := t.TempDir()

	// Write system prompt file.
	sysPromptPath := filepath.Join(t.TempDir(), "sys.md")
	require.NoError(t, os.WriteFile(sysPromptPath, []byte("You are a helpful assistant."), 0600))

	// Write memory file under memDir/telegram/memory.md.
	chanDir := filepath.Join(memDir, "telegram")
	require.NoError(t, os.MkdirAll(chanDir, 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(chanDir, "memory.md"),
		[]byte("Leo likes tea."),
		0600,
	))

	cfg := &config.Config{
		APIKeyEnv: "ANTHROPIC_API_KEY",
		StateDir:     stateDir,
		Concurrency:  2,
		Channels: map[string]config.Channel{
			"telegram": {
				Enabled:          true,
				AllowFrom:        []string{"alice"},
				SystemPromptFile: sysPromptPath,
			},
		},
	}
	sessions, err := NewSessionStore(stateDir)
	require.NoError(t, err)
	runner := NewClaudeRunner(bin, "sonnet")
	fb := NewFallbackRunner(runner, nil)
	d := NewDispatcher(cfg, NewAccess(cfg), sessions, memory.NewLoader(memDir), fb)

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi",
	})
	require.NoError(t, err)
	require.Equal(t, "ok", reply.Text)
}

func TestDispatcher_combineSystemPrompt_unit(t *testing.T) {
	require.Equal(t, "sys\n\nmem", combineSystemPrompt("sys", "mem"))
	require.Equal(t, "sys", combineSystemPrompt("sys", ""))
	require.Equal(t, "mem", combineSystemPrompt("", "mem"))
	require.Equal(t, "", combineSystemPrompt("", ""))
}

func TestDispatcher_systemPromptFileSizeCapEnforced(t *testing.T) {
	bin := mockClaudeScript(t, `{"result":"ok"}`, "", 0)

	stateDir := t.TempDir()
	memDir := t.TempDir()

	// Write a system prompt file larger than the 64KB cap.
	sysPromptPath := filepath.Join(t.TempDir(), "sys.md")
	big := make([]byte, 65*1024)
	for i := range big {
		big[i] = 'x'
	}
	require.NoError(t, os.WriteFile(sysPromptPath, big, 0600))

	cfg := &config.Config{
		APIKeyEnv: "ANTHROPIC_API_KEY",
		StateDir:     stateDir,
		Concurrency:  2,
		Channels: map[string]config.Channel{
			"telegram": {
				Enabled:          true,
				AllowFrom:        []string{"alice"},
				SystemPromptFile: sysPromptPath,
			},
		},
	}
	sessions, err := NewSessionStore(stateDir)
	require.NoError(t, err)
	runner := NewClaudeRunner(bin, "sonnet")
	fb := NewFallbackRunner(runner, nil)
	d := NewDispatcher(cfg, NewAccess(cfg), sessions, memory.NewLoader(memDir), fb)

	// Dispatch should still succeed (oversized prompt is logged and skipped,
	// not an error), and the reply should come through as normal — proving
	// the dispatcher degrades gracefully instead of stalling.
	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi",
	})
	require.NoError(t, err)
	require.Equal(t, "ok", reply.Text)
}

func TestDispatcher_combineSystemPrompt_trimsTrailingWhitespace(t *testing.T) {
	require.Equal(t, "sys\n\nmem", combineSystemPrompt("sys\n", "mem\n"))
	require.Equal(t, "sys\n\nmem", combineSystemPrompt("sys\n\n", "mem\n\n"))
	require.Equal(t, "sys", combineSystemPrompt("sys\n", ""))
	require.Equal(t, "mem", combineSystemPrompt("", "mem\n"))
}

func TestDispatcher_concurrencyCapSerializes(t *testing.T) {
	// Mock claude that sleeps for 150ms so we can observe serialization.
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/bash\nsleep 0.15\ncat <<'STDOUT'\n{\"result\":\"ok\"}\nSTDOUT\nexit 0\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0700))

	stateDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv: "ANTHROPIC_API_KEY",
		StateDir:     stateDir,
		Concurrency:  1, // force strict serialization
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	sessions, err := NewSessionStore(stateDir)
	require.NoError(t, err)
	runner := NewClaudeRunner(bin, "sonnet")
	fb := NewFallbackRunner(runner, nil)
	d := NewDispatcher(cfg, NewAccess(cfg), sessions, memory.NewLoader(t.TempDir()), fb)

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
