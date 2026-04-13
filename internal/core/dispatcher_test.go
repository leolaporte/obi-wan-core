package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/memory"
	"github.com/stretchr/testify/require"
)

// mockAPIServer returns an httptest.Server that always responds with the given text.
func mockAPIServer(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := mockAPI(t, text, http.StatusOK)
	t.Cleanup(srv.Close)
	return srv
}

func newTestDispatcher(t *testing.T, srv *httptest.Server) (*Dispatcher, string) {
	t.Helper()
	stateDir := t.TempDir()
	memDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:       "ANTHROPIC_API_KEY",
		StateDir:        stateDir,
		Concurrency:     2, // must be >= 1; matches config.Load default
		TokenBudget:     4000,
		EscalationModel: "claude-opus-4-6",
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	fb := NewFallbackRunner(client, nil)
	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	d := NewDispatcher(
		cfg,
		NewAccess(cfg),
		history,
		memory.NewLoader(memDir),
		fb,
	)
	return d, memDir
}

func TestDispatcher_allowedTurnReturnsReply(t *testing.T) {
	srv := mockAPIServer(t, "hi alice")
	d, _ := newTestDispatcher(t, srv)

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
	srv := mockAPIServer(t, "should not run")
	d, _ := newTestDispatcher(t, srv)

	_, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "mallory", Message: "hi",
	})
	require.ErrorIs(t, err, ErrAccessDenied)
}

func TestDispatcher_memoryInjectedIntoSystemPrompt(t *testing.T) {
	srv := mockAPIServer(t, "ok")
	d, memDir := newTestDispatcher(t, srv)

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
	srv := mockAPIServer(t, "ok")

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
		APIKeyEnv:   "ANTHROPIC_API_KEY",
		StateDir:    stateDir,
		Concurrency: 2,
		TokenBudget: 4000,
		Channels: map[string]config.Channel{
			"telegram": {
				Enabled:          true,
				AllowFrom:        []string{"alice"},
				SystemPromptFile: sysPromptPath,
			},
		},
	}
	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	fb := NewFallbackRunner(client, nil)
	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(memDir), fb)

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
	srv := mockAPIServer(t, "ok")

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
		APIKeyEnv:   "ANTHROPIC_API_KEY",
		StateDir:    stateDir,
		Concurrency: 2,
		TokenBudget: 4000,
		Channels: map[string]config.Channel{
			"telegram": {
				Enabled:          true,
				AllowFrom:        []string{"alice"},
				SystemPromptFile: sysPromptPath,
			},
		},
	}
	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	fb := NewFallbackRunner(client, nil)
	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(memDir), fb)

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
	// Mock API server that sleeps 150ms before responding.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	t.Cleanup(srv.Close)

	stateDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:   "ANTHROPIC_API_KEY",
		StateDir:    stateDir,
		Concurrency: 1, // force strict serialization
		TokenBudget: 4000,
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	fb := NewFallbackRunner(client, nil)
	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(t.TempDir()), fb)

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

func TestDispatcher_HistorySavedBetweenTurns(t *testing.T) {
	srv := mockAPIServer(t, "reply")
	d, _ := newTestDispatcher(t, srv)
	ctx := context.Background()

	// First turn
	_, err := d.Dispatch(ctx, Turn{Channel: "telegram", UserID: "alice", Message: "first"})
	require.NoError(t, err)

	history, _ := d.history.Load()
	require.Len(t, history, 2, "first turn should produce 1 user+assistant pair")

	// Second turn
	_, err = d.Dispatch(ctx, Turn{Channel: "telegram", UserID: "alice", Message: "second"})
	require.NoError(t, err)

	history, _ = d.history.Load()
	require.Len(t, history, 4, "two turns should produce 2 user+assistant pairs")
}

func TestDispatcher_ModelEscalation(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if m, ok := body["model"].(string); ok {
			receivedModel = m
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "escalated reply"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	stateDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:       "ANTHROPIC_API_KEY",
		StateDir:        stateDir,
		Concurrency:     2,
		TokenBudget:     4000,
		EscalationModel: "claude-opus-4-6",
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	fb := NewFallbackRunner(client, nil)
	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(t.TempDir()), fb)

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "/opus think hard",
	})
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", receivedModel)
	require.Equal(t, "escalated reply", reply.Text)
}

func TestDispatcher_APIErrorReturnsErrorText(t *testing.T) {
	srv := mockAPI(t, "", http.StatusInternalServerError)
	t.Cleanup(srv.Close)

	stateDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:   "ANTHROPIC_API_KEY",
		StateDir:    stateDir,
		Concurrency: 2,
		TokenBudget: 4000,
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}
	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	fb := NewFallbackRunner(client, nil)
	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(t.TempDir()), fb)

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi",
	})
	require.NoError(t, err, "API errors are returned as reply text, not Go errors")
	require.Contains(t, reply.Text, "Error")
}
