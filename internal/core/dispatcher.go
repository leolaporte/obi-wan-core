package core

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/memory"
)

// ErrAccessDenied is returned when a Turn is rejected by the allowlist.
var ErrAccessDenied = errors.New("access denied")

// Dispatcher is the shared core that all clients route turns through.
// It looks up the session ID, loads the memory file, invokes claude,
// handles session rotation on error, and returns a Reply.
type Dispatcher struct {
	cfg      *config.Config
	access   *Access
	sessions *SessionStore
	memory   *memory.Loader
	claude   *ClaudeRunner
	sem      chan struct{}
}

// NewDispatcher wires together the core pieces. Callers must pass a
// config produced by config.Load (or otherwise validated); Concurrency
// must be >= 1.
func NewDispatcher(
	cfg *config.Config,
	access *Access,
	sessions *SessionStore,
	memoryLoader *memory.Loader,
	claude *ClaudeRunner,
) *Dispatcher {
	return &Dispatcher{
		cfg:      cfg,
		access:   access,
		sessions: sessions,
		memory:   memoryLoader,
		claude:   claude,
		sem:      make(chan struct{}, cfg.Concurrency),
	}
}

// Dispatch processes one Turn and returns the Reply.
// Returns ErrAccessDenied if the user is not allowed on the channel.
func (d *Dispatcher) Dispatch(ctx context.Context, turn Turn) (*Reply, error) {
	if !d.access.Allowed(turn.Channel, turn.UserID) {
		slog.Warn("access denied", "channel", turn.Channel, "user", turn.UserID)
		return nil, ErrAccessDenied
	}

	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-d.sem }()

	sid, fresh := d.sessions.LoadOrCreate(turn.Channel)

	mem, err := d.memory.Load(turn.Channel)
	if err != nil {
		slog.Warn("memory load failed; continuing without",
			"channel", turn.Channel,
			"error", err,
		)
		mem = ""
	}

	var sysPrompt string
	if path := d.cfg.Channels[turn.Channel].SystemPromptFile; path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("system prompt file load failed; continuing without",
				"channel", turn.Channel,
				"path", path,
				"error", err,
			)
		} else {
			sysPrompt = string(data)
		}
	}

	combined := combineSystemPrompt(sysPrompt, mem)

	result, err := d.claude.Run(ctx, RunArgs{
		Message:      turn.Message,
		SessionID:    sid,
		IsNewSession: fresh,
		SystemPrompt: combined,
	})
	if err != nil {
		return nil, err
	}

	// Session error → rotate and retry ONCE.
	if result.SessionError {
		slog.Info("session error; rotating", "channel", turn.Channel)
		newSID := d.sessions.Rotate(turn.Channel)
		result, err = d.claude.Run(ctx, RunArgs{
			Message:      turn.Message,
			SessionID:    newSID,
			IsNewSession: true,
			SystemPrompt: combined,
		})
		if err != nil {
			return nil, err
		}
		if result.SessionError {
			return &Reply{Text: "Session rotation failed; please retry."}, nil
		}
	} else if fresh {
		d.sessions.MarkResumed(turn.Channel)
	}

	return &Reply{Text: result.Text}, nil
}

// combineSystemPrompt joins an optional system prompt and memory block
// with a paragraph break. Empty inputs are skipped.
func combineSystemPrompt(sysPrompt, mem string) string {
	switch {
	case sysPrompt == "" && mem == "":
		return ""
	case sysPrompt == "":
		return mem
	case mem == "":
		return sysPrompt
	default:
		return sysPrompt + "\n\n" + mem
	}
}
