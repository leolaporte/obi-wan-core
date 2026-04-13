package core

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/memory"
)

// ErrAccessDenied is returned when a Turn is rejected by the allowlist.
var ErrAccessDenied = errors.New("access denied")

// Dispatcher is the shared core that all clients route turns through.
// It looks up conversation history, loads the memory file, invokes the
// API client via FallbackRunner, and returns a Reply.
type Dispatcher struct {
	cfg      *config.Config
	access   *Access
	memory   *memory.Loader
	claude   *FallbackRunner
	sem      chan struct{}
}

// NewDispatcher wires together the core pieces. Callers must pass a
// config produced by config.Load (or otherwise validated); Concurrency
// must be >= 1.
func NewDispatcher(
	cfg *config.Config,
	access *Access,
	memory *memory.Loader,
	claude *FallbackRunner,
) *Dispatcher {
	return &Dispatcher{
		cfg:    cfg,
		access: access,
		memory: memory,
		claude: claude,
		sem:    make(chan struct{}, cfg.Concurrency),
	}
}

// Dispatch processes one Turn and returns the Reply.
// Returns ErrAccessDenied if the user is not allowed on the channel.
func (d *Dispatcher) Dispatch(ctx context.Context, turn Turn) (*Reply, error) {
	if !d.access.Allowed(turn.Channel, turn.UserID) {
		slog.Warn("access denied", "channel", turn.Channel, "user", turn.UserID)
		return nil, ErrAccessDenied
	}

	slog.Info("dispatch: acquiring semaphore", "channel", turn.Channel)
	select {
	case d.sem <- struct{}{}:
		slog.Info("dispatch: semaphore acquired", "channel", turn.Channel)
	case <-ctx.Done():
		slog.Warn("dispatch: context cancelled waiting for semaphore", "channel", turn.Channel)
		return nil, ctx.Err()
	}
	defer func() { <-d.sem }()

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
		sysPrompt = d.loadSystemPromptFile(turn.Channel, path)
	}

	combined := combineSystemPrompt(sysPrompt, mem)

	// Load conversation history for this channel.
	histPath := filepath.Join(d.cfg.StateDir, turn.Channel+".history.json")
	hist := NewHistory(histPath, d.cfg.TokenBudget)
	msgs, _ := hist.Load()
	msgs = hist.Prune(msgs)

	// Append the new user message.
	msgs = append(msgs, Message{Role: "user", Content: turn.Message})

	args := SendArgs{
		System:   combined,
		Messages: msgs,
	}

	slog.Info("dispatch: calling claude.Run", "channel", turn.Channel)
	text, err := d.claude.Run(ctx, args)
	if err != nil {
		return nil, err
	}

	// Persist updated history (user + assistant pair).
	updated := hist.Append(msgs[:len(msgs)-1], turn.Message, text)
	updated = hist.Prune(updated)
	if saveErr := hist.Save(updated); saveErr != nil {
		slog.Warn("history save failed", "channel", turn.Channel, "error", saveErr)
	}

	return &Reply{Text: text}, nil
}

// maxSystemPromptSize caps the on-disk size of a channel's system prompt
// file. Matches memory.MaxSize — system prompts are manually authored and
// should never approach this limit; anything larger signals a bug or
// runaway growth.
const maxSystemPromptSize = 64 * 1024

// loadSystemPromptFile reads the channel's system prompt file, returning
// an empty string on any failure (missing file, oversized file, read
// error). Failures are logged but do not block dispatch.
func (d *Dispatcher) loadSystemPromptFile(channel, path string) string {
	info, err := os.Stat(path)
	if err != nil {
		slog.Warn("system prompt file stat failed; continuing without",
			"channel", channel,
			"path", path,
			"error", err,
		)
		return ""
	}
	if info.Size() > maxSystemPromptSize {
		slog.Warn("system prompt file too large; continuing without",
			"channel", channel,
			"path", path,
			"size", info.Size(),
			"max", maxSystemPromptSize,
		)
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("system prompt file read failed; continuing without",
			"channel", channel,
			"path", path,
			"error", err,
		)
		return ""
	}
	return string(data)
}

// combineSystemPrompt joins an optional system prompt and memory block
// with a paragraph break. Trailing whitespace is trimmed from both
// inputs so a file ending in "\n" does not produce a triple newline in
// the combined output.
func combineSystemPrompt(sysPrompt, mem string) string {
	sysPrompt = strings.TrimRight(sysPrompt, " \t\r\n")
	mem = strings.TrimRight(mem, " \t\r\n")
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
