# Direct API Migration: Replace `claude -p` with Anthropic Messages API

**Date:** 2026-04-13
**Status:** Approved

## Problem

Every message through obi-wan-core spawns a `claude -p` subprocess that loads the full Claude Code environment — all tools, MCP servers, plugins, and system prompts — consuming ~47K tokens before the user's message is even considered. For conversational Telegram/Watch/R1/ESP32 messages, this is massive overhead that increases latency and cost.

## Solution

Replace the `claude -p` subprocess with direct HTTP calls to the Anthropic Messages API (`/v1/messages`). This applies to the primary tier and all fallback tiers — no more `claude -p` dependency anywhere.

## Architecture

```
Telegram/Watch/R1/ESP32
        |
    Dispatcher  (unchanged)
        |
   FallbackRunner  (unchanged shape, tiers are now APIClients)
        |
   APIClient.Send(system, history, message) -> response
        |
   +----------+
   | History  |  <- unified JSON file, token-budget pruning
   +----------+
```

### Key Components

**`APIClient`** — Stateless HTTP client parameterized by `(baseURL, apiKey, model)`. POSTs to `/v1/messages`, returns the text response. No retries — the FallbackRunner cascade handles that.

**`History`** — Manages a unified conversation history stored as a JSON file. Supports load, append, prune, and save. Shared across all channels (one Leo, one Obi-Wan). The channel source tag in each message (`[Source: watch]`) preserves device context.

**Model escalation** — Dispatcher scans messages for `/opus` prefix. If found, strips the prefix and overrides the model to `claude-opus-4-6` for that turn. Only applies to Anthropic-compatible tiers.

## Data Flow

1. Dispatcher receives Turn, checks access, acquires semaphore (unchanged)
2. Load system prompt + memory (unchanged)
3. Load history from `<state_dir>/history.json`
4. Check for `/opus` prefix — strip and flag if found
5. Build API request:
   - `system`: combined system prompt + memory
   - `messages`: history array + new user message (with `[Current time: ... | Source: channel]` prefix)
   - `model`: configured default or escalated
   - `max_tokens`: 4096
6. POST to `<base_url>/v1/messages` with `x-api-key` header and `anthropic-version: 2023-06-01`
7. Parse response, extract `content[0].text`
8. Append user message + assistant response to history
9. Prune if estimated tokens exceed budget (drop oldest message pairs from front)
10. Save history to disk
11. Return Reply to Dispatcher

On API failure, FallbackRunner tries next tier with the same history + message, different `(baseURL, apiKey, model)`.

## Conversation History

- **Storage:** Single JSON file at `<state_dir>/history.json`
- **Format:** Array of `{"role": "user"|"assistant", "content": "..."}` objects
- **Unified:** Shared across all channels — Watch, Telegram, R1, ESP32 all contribute to and read from the same history
- **Concurrency safety:** The dispatcher's existing semaphore serializes all dispatches, so no file locking needed
- **Token estimation:** `len(text) / 4` — approximate but sufficient for pruning decisions
- **Pruning strategy:** When total estimated tokens exceed `token_budget` (default 80,000), drop oldest message pairs from the front until under budget
- **Corruption/missing:** Start fresh with empty history on any load error. Log warning but don't fail.
- **Budget exceeded after pruning:** Clear history entirely, send current message alone

## Model Escalation

- Default model from config (e.g., `claude-sonnet-4-6`)
- `/opus` prefix in message triggers escalation to the model specified by `escalation_model` config field (defaults to `claude-opus-4-6`)
- Prefix is stripped before sending to the API
- On fallback tiers, escalation is ignored — tier uses its own configured model

## Error Handling

- **API errors (429, 500, overloaded):** Return error, let FallbackRunner cascade to next tier
- **No retries within a tier** — the fallback chain is the retry strategy
- **History file missing/corrupt:** Fresh start, log warning
- **Ollama compatibility:** Keep requests minimal — `model`, `system`, `messages`, `max_tokens`. No extended thinking, no tool use.

## What Changes

### Removed

- `ClaudeRunner` / `ClaudeRunnerWithEnv` (subprocess wrapper) — `claude.go`
- `RunArgs.SessionID`, `RunArgs.IsNewSession`, `RunResult.SessionError`
- `SessionStore` — `session.go`
- `isSessionError()`, session rotation logic in Dispatcher
- `claude_binary` config field
- `CI=1` environment variable injection

### New

- `APIClient` struct in `internal/core/api.go` — HTTP client for Messages API
- `History` struct in `internal/core/history.go` — load/append/prune/save
- `history_file` config field (defaults to `<state_dir>/history.json`)
- `token_budget` config field (defaults to 80,000)
- `api_key_env` and `base_url` in root config
- Model escalation check in Dispatcher

### Unchanged

- Dispatcher structure (access, semaphore, memory, system prompt combining)
- FallbackRunner shape (primary + N tiers, cascade on failure)
- All client code (Telegram, Watch, R1)
- Channel config structure
- Memory loader
- Access control

## Config Migration

```yaml
# Before
claude_binary: /home/leo/.local/bin/claude
model: sonnet

# After
api_key_env: ANTHROPIC_API_KEY
base_url: https://api.anthropic.com   # optional, this is the default
model: claude-sonnet-4-6
escalation_model: claude-opus-4-6     # optional, used by /opus prefix
token_budget: 80000                    # optional, default 80000

fallback:
  enabled: true
  tiers:
    - base_url: https://api.z.ai/api/anthropic
      api_key_env: ZAI_API_KEY
      model: glm-5.1
      label: GLM
    - base_url: http://localhost:11434
      auth_token_env: OLLAMA_AUTH_TOKEN
      model: gemma4:latest
      label: Ollama
```

## Expected Impact

- **Token usage:** ~47K → ~2-3K per message (system prompt + memory only)
- **Latency:** Eliminates subprocess startup overhead (~2-3s saved)
- **Dependencies:** Removes dependency on `claude` binary being installed
- **Binary size:** No change (no new Go dependencies)
