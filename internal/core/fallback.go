package core

import (
	"context"
	"fmt"
	"log/slog"
)

// FallbackTier is a labeled APIClient used as a fallback.
type FallbackTier struct {
	Client *APIClient
	Label  string
}

// FallbackRunner wraps a primary APIClient and zero or more fallback tiers.
type FallbackRunner struct {
	primary   *APIClient
	fallbacks []FallbackTier
}

// NewFallbackRunner creates a FallbackRunner.
func NewFallbackRunner(primary *APIClient, fallbacks []FallbackTier) *FallbackRunner {
	return &FallbackRunner{primary: primary, fallbacks: fallbacks}
}

// Run tries the primary client first. On any error, tries each fallback
// tier in order. If all fail, the last error is returned.
func (fr *FallbackRunner) Run(ctx context.Context, args SendArgs) (string, error) {
	text, err := fr.primary.Send(ctx, args)
	if err == nil {
		return text, nil
	}

	if len(fr.fallbacks) == 0 {
		return "", err
	}

	slog.Warn("primary failed; trying fallback chain",
		"primary_error", truncate(err.Error(), 200),
		"tiers", len(fr.fallbacks),
	)

	var lastErr error
	for i, fb := range fr.fallbacks {
		// Fallback tiers use their own model, so clear any override.
		fbArgs := SendArgs{
			System:   args.System,
			Messages: args.Messages,
		}
		text, fbErr := fb.Client.Send(ctx, fbArgs)
		if fbErr != nil {
			slog.Error("fallback tier failed",
				"tier", fb.Label, "index", i,
				"error", truncate(fbErr.Error(), 200),
			)
			lastErr = fmt.Errorf("[%s] %w", fb.Label, fbErr)
			continue
		}

		slog.Info("fallback succeeded", "tier", fb.Label, "index", i)
		return fmt.Sprintf("[%s] %s", fb.Label, text), nil
	}

	return "", lastErr
}
