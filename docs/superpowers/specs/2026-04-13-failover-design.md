# z.ai Failover for obi-wan-core

**Date:** 2026-04-13
**Status:** Draft

## Problem

When Anthropic's API is down or returns errors (401, 429, 500, 503), obi-wan-core's `ClaudeRunner` fails and the user gets an error message. Leo has a z.ai Max coding plan that exposes an Anthropic-compatible API at `https://api.z.ai/api/anthropic` with GLM models.

## Solution

Add a `FallbackRunner` that wraps two `ClaudeRunner` instances — primary (Anthropic) and fallback (z.ai). Any failure from the primary triggers an automatic retry against the fallback provider. Fallback replies are prefixed with `[GLM]` so the user knows which model responded.

## Architecture

```
Dispatcher → FallbackRunner.Run()
                ├─ primary.Run()   (claude -p, default env)
                │   └─ any failure? → fallback.Run() (claude -p, z.ai env)
                └─ success → return result
```

`FallbackRunner` holds two `*ClaudeRunner` instances and exposes the same `Run(ctx, RunArgs) (*RunResult, error)` method. No interface — just a concrete struct swap in the dispatcher.

## Config

New fields in `config.yaml`:

```yaml
claude_binary: /home/leo/.local/bin/claude
model: sonnet
fallback:
  enabled: true
  base_url: https://api.z.ai/api/anthropic
  api_key_env: ZAI_API_KEY
  model: glm-5.1
```

- `model` moves from hardcoded `"sonnet"` in `main.go` into config
- `fallback.enabled` — when false, `FallbackRunner` is a no-op wrapper that only uses primary
- `fallback.base_url` — set as `ANTHROPIC_BASE_URL` env var on the fallback subprocess
- `fallback.api_key_env` — env var name containing the z.ai API key
- `fallback.model` — model name for the fallback provider

## Behavior

### Primary success

Normal flow. `claude -p` exits 0, result is parsed and returned unchanged.

### Primary failure

Any non-zero exit from `claude -p` (auth errors, rate limits, server errors, network failures) triggers fallback:

1. Log: `slog.Warn("primary failed; falling back", "stderr", truncate(stderr, 200))`
2. Retry the same `RunArgs` against the fallback runner with:
   - `ANTHROPIC_BASE_URL` = `fallback.base_url`
   - `ANTHROPIC_API_KEY` = value from `fallback.api_key_env`
   - `--model` = `fallback.model`
   - `--session-id` with a fresh UUID (no session continuity across providers)
3. On success: prefix reply with `[GLM]`, log `slog.Info("fallback succeeded")`
4. On failure: return the fallback's error message, also prefixed with `[GLM]`

### Fallback disabled

When `fallback.enabled` is false or the config section is absent, `FallbackRunner` passes through to primary only. No retry, no env var manipulation.

## Fallback session handling

The fallback always starts a fresh session (`--session-id` with new UUID, not `--resume`). Different providers cannot share session state. Acceptable because Telegram/Watch/R1 messages are mostly self-contained.

## Error detection

No specific error matching. Any non-zero exit from primary triggers fallback. This covers:
- 401/403 authentication failures
- 429 rate limiting
- 500/503 server errors
- Network timeouts
- Binary not found or other OS-level errors

The only exception: `SessionError` results from the primary do NOT trigger fallback — they go through the existing session rotation path in the dispatcher.

## Logging

| Event | Level | Message |
|-------|-------|---------|
| Primary failed | Warn | `"primary failed; falling back"` + truncated stderr |
| Fallback succeeded | Info | `"fallback succeeded"` + provider + model |
| Fallback also failed | Error | `"fallback also failed"` + stderr |
| Fallback disabled, primary failed | Error | Existing error path, unchanged |

## Files changed

| File | Change |
|------|--------|
| `internal/core/fallback.go` | **New** — `FallbackRunner` struct, `Run()` method, `NewFallbackRunner()` constructor |
| `internal/core/fallback_test.go` | **New** — unit tests for pass-through, fallback trigger, double-failure |
| `internal/core/claude.go` | Add `extraEnv` field to `ClaudeRunner`; `Run()` appends it to `cmd.Env`. Constructor `NewClaudeRunnerWithEnv()` for the fallback runner. |
| `internal/config/config.go` | Add `Model string` and `Fallback FallbackConfig` fields, validation |
| `config.yaml.example` | Add `model` and `fallback` section |
| `cmd/obi-wan-core/main.go` | Wire `FallbackRunner` instead of `ClaudeRunner` in `buildDispatcherWithConfig()` |

## Not in scope

- Provider health checks or pre-emptive switching
- Multiple fallback providers (chain)
- Session migration between providers
- Cost tracking or budget limits per provider
- Metrics/alerting on failover frequency
