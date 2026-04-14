package core

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
)

// History manages conversation history on disk with token-budget pruning.
type History struct {
	path        string
	tokenBudget int
}

// NewHistory constructs a History backed by the given file path.
// tokenBudget is the maximum estimated token count to keep in history.
func NewHistory(path string, tokenBudget int) *History {
	return &History{
		path:        path,
		tokenBudget: tokenBudget,
	}
}

// Load reads the history file from disk. A missing file returns an empty slice
// (not an error). A corrupt JSON file logs a warning and returns an empty slice.
func (h *History) Load() ([]Message, error) {
	data, err := os.ReadFile(h.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		slog.Warn("history load failed; starting fresh", "path", h.path, "error", err)
		return nil, nil
	}

	var msgs []Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		slog.Warn("history corrupt; starting fresh", "path", h.path, "error", err)
		return nil, nil
	}
	return msgs, nil
}

// Save writes the message slice to disk as JSON with 0600 permissions.
func (h *History) Save(msgs []Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	return os.WriteFile(h.path, data, 0600)
}

// Append adds a user+assistant pair to msgs and returns the updated slice.
func (h *History) Append(msgs []Message, userMsg, assistantMsg string) []Message {
	msgs = append(msgs, Message{Role: "user", Content: userMsg})
	msgs = append(msgs, Message{Role: "assistant", Content: assistantMsg})
	return msgs
}

// Prune drops the oldest message pairs from the front of msgs until the
// estimated token count is within budget. Messages are assumed to come in
// pairs (user+assistant). If even a single pair exceeds the budget, nil
// is returned.
func (h *History) Prune(msgs []Message) []Message {
	for estimateTokens(msgs) > h.tokenBudget && len(msgs) >= 2 {
		msgs = msgs[2:]
	}
	if estimateTokens(msgs) > h.tokenBudget {
		return nil
	}
	return msgs
}

// estimateTokens estimates the token count of a message slice as len(content)/4.
func estimateTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
	}
	return total
}
