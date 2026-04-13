package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// fallbackTier is a labeled ClaudeRunner used as a fallback when the primary
// (or a previous tier) fails.
type fallbackTier struct {
	runner *ClaudeRunner
	label  string
}

// FallbackRunner wraps a primary ClaudeRunner and zero or more fallback tiers.
// If the primary fails with a non-zero exit (any error other than a session
// error), each fallback tier is tried in order. Fallback replies are prefixed
// with the tier's label (e.g. "[GLM]").
type FallbackRunner struct {
	primary   *ClaudeRunner
	fallbacks []fallbackTier
}

// NewFallbackRunner creates a FallbackRunner. Each fallback tier is a
// ClaudeRunner with a label used as prefix on fallback replies (e.g. "GLM" ->
// "[GLM] reply text"). Pass nil or empty fallbacks to disable failover.
func NewFallbackRunner(primary *ClaudeRunner, fallbacks []fallbackTier) *FallbackRunner {
	return &FallbackRunner{
		primary:   primary,
		fallbacks: fallbacks,
	}
}

// FallbackTierConfig is a convenience type for constructing fallback tiers
// without exposing the internal struct.
type FallbackTierConfig struct {
	Runner *ClaudeRunner
	Label  string
}

// NewFallbackTier creates a fallbackTier from a config.
func NewFallbackTier(cfg FallbackTierConfig) fallbackTier {
	return fallbackTier{runner: cfg.Runner, label: cfg.Label}
}

// BuildFallbackTiers converts exported tier configs into the internal slice.
func BuildFallbackTiers(cfgs []FallbackTierConfig) []fallbackTier {
	tiers := make([]fallbackTier, len(cfgs))
	for i, c := range cfgs {
		tiers[i] = fallbackTier{runner: c.Runner, label: c.Label}
	}
	return tiers
}

// Run tries the primary runner first. On any non-session-error failure, it
// tries each fallback tier in order with a fresh session. If all fallbacks
// fail, the last fallback's error is returned (prefixed).
func (fr *FallbackRunner) Run(ctx context.Context, args RunArgs) (*RunResult, error) {
	result, err := fr.primary.Run(ctx, args)
	if err != nil {
		return nil, err
	}

	// Session errors are handled by the dispatcher's rotation logic.
	if result.SessionError {
		return result, nil
	}

	// If primary succeeded, return as-is.
	if !strings.HasPrefix(result.Text, "Error running claude:") {
		return result, nil
	}

	// No fallbacks configured — return primary error.
	if len(fr.fallbacks) == 0 {
		return result, nil
	}

	slog.Warn("primary failed; trying fallback chain",
		"primary_error", truncate(result.Text, 200),
		"tiers", len(fr.fallbacks),
	)

	var lastResult *RunResult
	for i, fb := range fr.fallbacks {
		fbResult, fbErr := fb.runner.Run(ctx, RunArgs{
			Message:      args.Message,
			Channel:      args.Channel,
			SessionID:    fmt.Sprintf("fallback-%d", time.Now().UnixNano()),
			IsNewSession: true,
			SystemPrompt: args.SystemPrompt,
		})
		if fbErr != nil {
			slog.Error("fallback subprocess failed to start",
				"tier", fb.label,
				"index", i,
				"error", fbErr,
			)
			continue
		}

		prefixed := fmt.Sprintf("[%s] %s", fb.label, fbResult.Text)

		if strings.HasPrefix(fbResult.Text, "Error running claude:") {
			slog.Error("fallback tier also failed",
				"tier", fb.label,
				"index", i,
				"fallback_error", truncate(fbResult.Text, 200),
			)
			lastResult = &RunResult{Text: prefixed}
			continue
		}

		slog.Info("fallback succeeded", "tier", fb.label, "index", i)
		return &RunResult{Text: prefixed}, nil
	}

	// All fallbacks failed — return last fallback's prefixed error.
	if lastResult != nil {
		return lastResult, nil
	}

	// All fallbacks failed to start — return primary error.
	return result, nil
}
