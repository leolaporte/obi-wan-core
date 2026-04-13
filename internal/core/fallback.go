package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// FallbackRunner wraps a primary and fallback ClaudeRunner. If the primary
// fails with a non-zero exit (any error other than a session error), the
// fallback runner is tried with a fresh session. Fallback replies are
// prefixed with the provider label (e.g. "[GLM]").
type FallbackRunner struct {
	primary  *ClaudeRunner
	fallback *ClaudeRunner
	label    string
}

// NewFallbackRunner creates a FallbackRunner. The label is used as a prefix
// on fallback replies (e.g. "GLM" -> "[GLM] reply text"). Pass nil for
// fallback to disable automatic failover.
func NewFallbackRunner(primary, fallback *ClaudeRunner, label string) *FallbackRunner {
	return &FallbackRunner{
		primary:  primary,
		fallback: fallback,
		label:    label,
	}
}

// Run tries the primary runner first. On any non-session-error failure, it
// retries with the fallback runner using a fresh session. If fallback is nil,
// the primary result is returned as-is.
func (fr *FallbackRunner) Run(ctx context.Context, args RunArgs) (*RunResult, error) {
	result, err := fr.primary.Run(ctx, args)
	if err != nil {
		return nil, err
	}

	// Session errors are handled by the dispatcher's rotation logic.
	if result.SessionError {
		return result, nil
	}

	// If primary returned an error (non-zero exit), try fallback.
	if fr.fallback != nil && strings.HasPrefix(result.Text, "Error running claude:") {
		slog.Warn("primary failed; falling back",
			"primary_error", truncate(result.Text, 200),
		)

		fbResult, fbErr := fr.fallback.Run(ctx, RunArgs{
			Message:      args.Message,
			Channel:      args.Channel,
			SessionID:    fmt.Sprintf("fallback-%d", time.Now().UnixNano()),
			IsNewSession: true,
			SystemPrompt: args.SystemPrompt,
		})
		if fbErr != nil {
			slog.Error("fallback subprocess failed to start", "error", fbErr)
			return result, nil
		}

		prefixed := fmt.Sprintf("[%s] %s", fr.label, fbResult.Text)

		if strings.HasPrefix(fbResult.Text, "Error running claude:") {
			slog.Error("fallback also failed",
				"fallback_error", truncate(fbResult.Text, 200),
			)
			return &RunResult{Text: prefixed}, nil
		}

		slog.Info("fallback succeeded", "label", fr.label)
		return &RunResult{Text: prefixed}, nil
	}

	return result, nil
}
