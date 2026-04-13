package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/memory"
)

// ErrAccessDenied is returned when a Turn is rejected by the allowlist.
var ErrAccessDenied = errors.New("access denied")

const opusPrefix = "/opus "

// Dispatcher is the shared core that all clients route turns through.
// It looks up conversation history, loads the memory file, invokes the
// API client via FallbackRunner, and returns a Reply.
type Dispatcher struct {
	cfg     *config.Config
	access  *Access
	history *History
	memory  *memory.Loader
	claude  *FallbackRunner
	sem     chan struct{}
}

// NewDispatcher wires together the core pieces. Callers must pass a
// config produced by config.Load (or otherwise validated); Concurrency
// must be >= 1.
func NewDispatcher(
	cfg *config.Config,
	access *Access,
	history *History,
	memoryLoader *memory.Loader,
	claude *FallbackRunner,
) *Dispatcher {
	return &Dispatcher{
		cfg:     cfg,
		access:  access,
		history: history,
		memory:  memoryLoader,
		claude:  claude,
		sem:     make(chan struct{}, cfg.Concurrency),
	}
}

// Dispatch processes one Turn and returns the Reply.
// Returns ErrAccessDenied if the user is not allowed on the channel.
func (d *Dispatcher) Dispatch(ctx context.Context, turn Turn) (*Reply, error) {
	// Access check
	if !d.access.Allowed(turn.Channel, turn.UserID) {
		slog.Warn("access denied", "channel", turn.Channel, "user", turn.UserID)
		return nil, ErrAccessDenied
	}

	// Semaphore
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-d.sem }()

	// Memory + system prompt
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

	// Load history
	history, err := d.history.Load()
	if err != nil {
		slog.Warn("history load failed; continuing with empty", "error", err)
		history = nil
	}

	// Model escalation
	message := turn.Message
	model := ""
	if strings.HasPrefix(message, opusPrefix) {
		message = strings.TrimPrefix(message, opusPrefix)
		model = d.cfg.EscalationModel
		slog.Info("model escalation triggered", "channel", turn.Channel, "model", model)
	}

	// Time/source injection
	now := time.Now().In(mustLoadLA())
	dated := fmt.Sprintf("[Current time: %s | Source: %s]\n\n%s",
		now.Format("Monday, January 2, 2006 3:04 PM"), turn.Channel, message)

	// Build messages
	msgs := make([]Message, len(history), len(history)+1)
	copy(msgs, history)
	msgs = append(msgs, Message{Role: "user", Content: dated})

	// Call API
	slog.Info("dispatch: calling API", "channel", turn.Channel, "history_len", len(history))
	text, err := d.claude.Run(ctx, SendArgs{
		System:   combined,
		Messages: msgs,
		Model:    model,
	})
	if err != nil {
		return &Reply{Text: fmt.Sprintf("Error: %s", truncate(err.Error(), 200))}, nil
	}

	// Update history
	history = d.history.Append(history, dated, text)
	history = d.history.Prune(history)
	if saveErr := d.history.Save(history); saveErr != nil {
		slog.Error("history save failed", "error", saveErr)
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

// mustLoadLA returns America/Los_Angeles, falling back to UTC if tzdata
// is missing.
func mustLoadLA() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.UTC
	}
	return loc
}

// truncate returns the first n bytes of s, or s itself if shorter.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
