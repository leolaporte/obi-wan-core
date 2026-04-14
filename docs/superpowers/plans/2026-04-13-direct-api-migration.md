# Direct API Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `claude -p` subprocess calls with direct Anthropic Messages API HTTP calls, unified conversation history, and model escalation.

**Architecture:** New `APIClient` (HTTP POST to `/v1/messages`) replaces `ClaudeRunner` (subprocess). New `History` manages a unified JSON file of conversation turns with token-budget pruning. `FallbackRunner` keeps its cascade shape but wraps `APIClient` instances instead of `ClaudeRunner` instances. Dispatcher drops session management, gains history loading/saving and `/opus` model escalation.

**Tech Stack:** Go stdlib (`net/http`, `encoding/json`, `net/http/httptest` for tests), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-13-direct-api-migration-design.md`

---

### File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/core/history.go` | Conversation history: load, append, prune, save |
| Create | `internal/core/history_test.go` | History unit tests |
| Create | `internal/core/api.go` | HTTP client for Anthropic Messages API |
| Create | `internal/core/api_test.go` | APIClient tests with httptest mock server |
| Modify | `internal/config/config.go` | Add `APIKeyEnv`, `BaseURL`, `TokenBudget`, `EscalationModel`; remove `ClaudeBinary` |
| Modify | `internal/config/config_test.go` | Update config tests for new fields |
| Modify | `internal/core/fallback.go` | Rewrite to wrap `APIClient` instead of `ClaudeRunner` |
| Modify | `internal/core/fallback_test.go` | Rewrite fallback tests with mock HTTP |
| Modify | `internal/core/turn.go` | Add `Message` type for history entries |
| Modify | `internal/core/dispatcher.go` | Integrate History, model escalation, remove session logic |
| Modify | `internal/core/dispatcher_test.go` | Rewrite dispatcher tests for new architecture |
| Modify | `cmd/obi-wan-core/main.go` | Wire `APIClient`, `History`; remove `SessionStore`, `ClaudeRunner` |
| Delete | `internal/core/claude.go` | Subprocess wrapper (replaced by api.go) |
| Delete | `internal/core/claude_test.go` | Subprocess tests (replaced by api_test.go) |
| Delete | `internal/core/session.go` | Session store (replaced by history.go) |
| Delete | `internal/core/session_test.go` | Session tests (replaced by history_test.go) |

---

### Task 1: History Store

**Files:**
- Create: `internal/core/history.go`
- Create: `internal/core/history_test.go`
- Modify: `internal/core/turn.go`

This is the foundational data layer — no external dependencies, pure file I/O + pruning logic.

- [ ] **Step 1: Add Message type to turn.go**

Add the `Message` type that represents a single conversation turn in history:

```go
// Message is a single turn in the conversation history, stored on disk
// and sent to the Messages API.
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}
```

Append this after the `Reply` struct in `internal/core/turn.go`.

- [ ] **Step 2: Write failing test for History.Load on missing file**

Create `internal/core/history_test.go`:

```go
package core

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistory_LoadMissing_ReturnsEmpty(t *testing.T) {
	h := NewHistory(filepath.Join(t.TempDir(), "history.json"), 80000)
	msgs, err := h.Load()
	require.NoError(t, err, "missing file is not an error")
	require.Empty(t, msgs)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_LoadMissing -v`
Expected: FAIL — `NewHistory` undefined.

- [ ] **Step 4: Implement History struct with Load**

Create `internal/core/history.go`:

```go
package core

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
)

// History manages a unified conversation history stored as a JSON file.
// All channels share one history — one Leo, one Obi-Wan.
type History struct {
	path        string
	tokenBudget int
}

// NewHistory creates a History backed by the given file path.
// tokenBudget is the approximate max tokens before pruning (uses len/4 estimate).
func NewHistory(path string, tokenBudget int) *History {
	return &History{path: path, tokenBudget: tokenBudget}
}

// Load reads the history from disk. Returns an empty slice if the file
// does not exist or is corrupt (logged but not an error).
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_LoadMissing -v`
Expected: PASS

- [ ] **Step 6: Write failing test for Save and Load round-trip**

Append to `internal/core/history_test.go`:

```go
func TestHistory_SaveAndLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	h := NewHistory(path, 80000)

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	require.NoError(t, h.Save(msgs))

	loaded, err := h.Load()
	require.NoError(t, err)
	require.Equal(t, msgs, loaded)
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_SaveAndLoad -v`
Expected: FAIL — `h.Save` undefined.

- [ ] **Step 8: Implement Save**

Add to `internal/core/history.go`:

```go
// Save writes the message history to disk as JSON.
func (h *History) Save(msgs []Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	return os.WriteFile(h.path, data, 0600)
}
```

- [ ] **Step 9: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_SaveAndLoad -v`
Expected: PASS

- [ ] **Step 10: Write failing test for Append**

Append to `internal/core/history_test.go`:

```go
func TestHistory_Append_AddsUserAndAssistantPair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	h := NewHistory(path, 80000)

	existing := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "first reply"},
	}

	result := h.Append(existing, "second", "second reply")
	require.Len(t, result, 4)
	require.Equal(t, "second", result[2].Content)
	require.Equal(t, "user", result[2].Role)
	require.Equal(t, "second reply", result[3].Content)
	require.Equal(t, "assistant", result[3].Role)
}
```

- [ ] **Step 11: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_Append -v`
Expected: FAIL — `h.Append` undefined.

- [ ] **Step 12: Implement Append**

Add to `internal/core/history.go`:

```go
// Append adds a user+assistant message pair to the history and returns
// the updated slice.
func (h *History) Append(msgs []Message, userMsg, assistantMsg string) []Message {
	return append(msgs,
		Message{Role: "user", Content: userMsg},
		Message{Role: "assistant", Content: assistantMsg},
	)
}
```

- [ ] **Step 13: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_Append -v`
Expected: PASS

- [ ] **Step 14: Write failing test for Prune**

Append to `internal/core/history_test.go`:

```go
func TestHistory_Prune_DropsOldestPairsWhenOverBudget(t *testing.T) {
	// Token budget of 100 chars / 4 = 25 tokens.
	// Each message is ~50 chars = ~12 tokens. A pair is ~24 tokens.
	// Two pairs = ~48 tokens, over budget of 25. Should drop the oldest pair.
	h := NewHistory("unused", 25)

	msgs := []Message{
		{Role: "user", Content: "this is the first message which is old"},
		{Role: "assistant", Content: "this is the first reply which is old"},
		{Role: "user", Content: "this is the second message which is new"},
		{Role: "assistant", Content: "this is the second reply which is new"},
	}

	pruned := h.Prune(msgs)
	require.Len(t, pruned, 2, "should drop oldest pair")
	require.Equal(t, "this is the second message which is new", pruned[0].Content)
}

func TestHistory_Prune_EmptyOnAllOverBudget(t *testing.T) {
	// Budget so tiny nothing fits.
	h := NewHistory("unused", 1)

	msgs := []Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}

	pruned := h.Prune(msgs)
	require.Empty(t, pruned, "should clear all when even one pair exceeds budget")
}

func TestHistory_Prune_NoPruneWhenUnderBudget(t *testing.T) {
	h := NewHistory("unused", 100000)

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	pruned := h.Prune(msgs)
	require.Len(t, pruned, 2, "should not prune when under budget")
}
```

- [ ] **Step 15: Run tests to verify they fail**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_Prune -v`
Expected: FAIL — `h.Prune` undefined.

- [ ] **Step 16: Implement Prune**

Add to `internal/core/history.go`:

```go
// estimateTokens approximates token count as len(text)/4.
func estimateTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
	}
	return total
}

// Prune drops the oldest message pairs (user+assistant) from the front
// until the estimated token count is within the budget. Returns a new slice.
func (h *History) Prune(msgs []Message) []Message {
	for estimateTokens(msgs) > h.tokenBudget && len(msgs) >= 2 {
		msgs = msgs[2:]
	}
	// If a single pair still exceeds budget, clear everything.
	if estimateTokens(msgs) > h.tokenBudget {
		return nil
	}
	return msgs
}
```

- [ ] **Step 17: Run tests to verify they pass**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_Prune -v`
Expected: PASS

- [ ] **Step 18: Write test for corrupt file handling**

Append to `internal/core/history_test.go`:

```go
func TestHistory_LoadCorrupt_ReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.WriteFile(path, []byte("not json{{{"), 0600))

	h := NewHistory(path, 80000)
	msgs, err := h.Load()
	require.NoError(t, err, "corrupt file is not an error")
	require.Empty(t, msgs)
}
```

Add `"os"` to the import block.

- [ ] **Step 19: Run test to verify it passes** (already handled by Load implementation)

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory_LoadCorrupt -v`
Expected: PASS

- [ ] **Step 20: Run all History tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestHistory -v`
Expected: All PASS

- [ ] **Step 21: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/core/turn.go internal/core/history.go internal/core/history_test.go
git commit -m "feat(core): add History store with load, save, append, and token-budget pruning"
```

---

### Task 2: APIClient

**Files:**
- Create: `internal/core/api.go`
- Create: `internal/core/api_test.go`

HTTP client that POSTs to the Anthropic Messages API. Tested with `httptest.NewServer` — no real API calls in tests.

- [ ] **Step 1: Write failing test for successful API call**

Create `internal/core/api_test.go`:

```go
package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIClient_Send_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "test-key", r.Header.Get("x-api-key"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "claude-sonnet-4-6", body["model"])
		require.Equal(t, "You are Obi-Wan.", body["system"])

		msgs := body["messages"].([]any)
		require.Len(t, msgs, 1)
		msg := msgs[0].(map[string]any)
		require.Equal(t, "user", msg["role"])
		require.Equal(t, "hello", msg["content"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello there."},
			},
		})
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-key", "claude-sonnet-4-6")
	resp, err := client.Send(context.Background(), SendArgs{
		System:   "You are Obi-Wan.",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello there.", resp)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient_Send_Success -v`
Expected: FAIL — `NewAPIClient` undefined.

- [ ] **Step 3: Implement APIClient**

Create `internal/core/api.go`:

```go
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// APIClient is a stateless HTTP client for the Anthropic Messages API.
// It is parameterized by (baseURL, apiKey, model) and reused across calls.
type APIClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewAPIClient constructs an APIClient.
// baseURL should be like "https://api.anthropic.com" (no trailing slash).
func NewAPIClient(baseURL, apiKey, model string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{},
	}
}

// SendArgs bundles per-call parameters for Send.
type SendArgs struct {
	System   string    // system prompt (optional)
	Messages []Message // conversation history including the new user message
	Model    string    // override model (empty = use client default)
}

// apiRequest is the JSON body sent to /v1/messages.
type apiRequest struct {
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
}

// apiResponse is the JSON body returned from /v1/messages.
type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Send makes one call to the Messages API and returns the text response.
func (c *APIClient) Send(ctx context.Context, args SendArgs) (string, error) {
	model := c.model
	if args.Model != "" {
		model = args.Model
	}

	reqBody := apiRequest{
		Model:     model,
		System:    args.System,
		Messages:  args.Messages,
		MaxTokens: 4096,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var parsed apiResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("API error: %s: %s", parsed.Error.Type, parsed.Error.Message)
	}

	for _, block := range parsed.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("no text content in response")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient_Send_Success -v`
Expected: PASS

- [ ] **Step 5: Write failing test for model override**

Append to `internal/core/api_test.go`:

```go
func TestAPIClient_Send_ModelOverride(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel = body["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "deep thought"},
			},
		})
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "key", "claude-sonnet-4-6")
	_, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "think hard"}},
		Model:    "claude-opus-4-6",
	})
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", receivedModel)
}
```

- [ ] **Step 6: Run test to verify it passes** (already handled by implementation)

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient_Send_ModelOverride -v`
Expected: PASS

- [ ] **Step 7: Write failing test for API error response**

Append to `internal/core/api_test.go`:

```go
func TestAPIClient_Send_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"too fast"}}`))
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "key", "claude-sonnet-4-6")
	_, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "429")
}
```

- [ ] **Step 8: Run test to verify it passes** (already handled)

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient_Send_HTTPError -v`
Expected: PASS

- [ ] **Step 9: Write test for conversation history passthrough**

Append to `internal/core/api_test.go`:

```go
func TestAPIClient_Send_HistoryPassedThrough(t *testing.T) {
	var receivedMsgs []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedMsgs = body["messages"].([]any)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "ok"},
			},
		})
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "key", "claude-sonnet-4-6")
	_, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "first reply"},
			{Role: "user", Content: "second"},
		},
	})
	require.NoError(t, err)
	require.Len(t, receivedMsgs, 3)
}
```

- [ ] **Step 10: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient_Send_History -v`
Expected: PASS

- [ ] **Step 11: Run all APIClient tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient -v`
Expected: All PASS

- [ ] **Step 12: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/core/api.go internal/core/api_test.go
git commit -m "feat(core): add APIClient for direct Anthropic Messages API calls"
```

---

### Task 3: Config Migration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

Add new fields, remove `ClaudeBinary`. This is a breaking config change — the old `claude_binary` field is no longer recognized.

- [ ] **Step 1: Write failing test for new config fields**

Replace the `TestLoad_minimalValid` test in `internal/config/config_test.go` with:

```go
func TestLoad_minimalValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
channels:
  telegram:
    enabled: true
    allow_from:
      - "123456"
  watch:
    enabled: true
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "ANTHROPIC_API_KEY", cfg.APIKeyEnv)
	require.Equal(t, "/tmp/obi-wan-core-test", cfg.StateDir)
	require.Equal(t, "https://api.anthropic.com", cfg.BaseURL, "default base URL")
	require.Equal(t, "claude-sonnet-4-6", cfg.Model, "default model")
	require.Equal(t, 80000, cfg.TokenBudget, "default token budget")
	require.Equal(t, "claude-opus-4-6", cfg.EscalationModel, "default escalation model")
	require.True(t, cfg.Channels["telegram"].Enabled)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/config/ -run TestLoad_minimalValid -v`
Expected: FAIL — `cfg.APIKeyEnv` undefined.

- [ ] **Step 3: Update Config struct**

Replace the `Config` struct in `internal/config/config.go`:

```go
// Config is the root config structure loaded from YAML.
type Config struct {
	APIKeyEnv       string             `yaml:"api_key_env"`
	BaseURL         string             `yaml:"base_url"`
	StateDir        string             `yaml:"state_dir"`
	Concurrency     int                `yaml:"concurrency"`
	Model           string             `yaml:"model"`
	EscalationModel string             `yaml:"escalation_model"`
	TokenBudget     int                `yaml:"token_budget"`
	Fallback        FallbackConfig     `yaml:"fallback"`
	Channels        map[string]Channel `yaml:"channels"`
}
```

- [ ] **Step 4: Update validation in Load function**

Replace the validation logic in `Load()` (the block after `yaml.Unmarshal`):

```go
	if cfg.APIKeyEnv == "" {
		return nil, fmt.Errorf("config: api_key_env is required")
	}
	if cfg.StateDir == "" {
		return nil, fmt.Errorf("config: state_dir is required")
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}

	if cfg.Concurrency == 0 {
		cfg.Concurrency = 2
	}

	if cfg.Concurrency < 1 {
		return nil, fmt.Errorf("config: concurrency must be >= 1, got %d", cfg.Concurrency)
	}

	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}

	if cfg.EscalationModel == "" {
		cfg.EscalationModel = "claude-opus-4-6"
	}

	if cfg.TokenBudget == 0 {
		cfg.TokenBudget = 80000
	}

	for name, ch := range cfg.Channels {
		if ch.OpenAccess && len(ch.AllowFrom) > 0 {
			return nil, fmt.Errorf("config: channel %q has both open_access and allow_from set; remove allow_from", name)
		}
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/config/ -run TestLoad_minimalValid -v`
Expected: PASS

- [ ] **Step 6: Update remaining config tests**

Update all tests in `internal/config/config_test.go` that reference `claude_binary` to use `api_key_env` instead. In each test's YAML content:
- Replace `claude_binary: /home/leo/.local/bin/claude` with `api_key_env: ANTHROPIC_API_KEY`
- Replace `claude_binary: /bin/true` with `api_key_env: ANTHROPIC_API_KEY`

Update `TestLoad_missingRequiredField` to test for missing `api_key_env`:

```go
func TestLoad_missingRequiredField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
state_dir: /tmp/test
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	_, err := Load(path)
	require.Error(t, err, "should reject config missing api_key_env")
}
```

Update `TestLoad_clientFields` to use `api_key_env` and remove the `ClaudeBinary` assertion:

```go
func TestLoad_clientFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
concurrency: 3
channels:
  telegram:
    enabled: true
    allow_from: ["123456789"]
    system_prompt_file: /home/leo/.claude/channels/telegram/system-prompt.md
    bot_token_env: TELEGRAM_BOT_TOKEN
  watch:
    enabled: true
    open_access: true
    webhook_port: 8199
    webhook_key_env: WEBHOOK_KEY
    watch_chat_id_env: WATCH_CHAT_ID
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 3, cfg.Concurrency)
	require.Equal(t, "/home/leo/.claude/channels/telegram/system-prompt.md", cfg.Channels["telegram"].SystemPromptFile)
	require.Equal(t, "TELEGRAM_BOT_TOKEN", cfg.Channels["telegram"].BotTokenEnv)
	require.True(t, cfg.Channels["watch"].OpenAccess)
	require.Equal(t, 8199, cfg.Channels["watch"].WebhookPort)
	require.Equal(t, "WEBHOOK_KEY", cfg.Channels["watch"].WebhookKeyEnv)
	require.Equal(t, "WATCH_CHAT_ID", cfg.Channels["watch"].WatchChatIDEnv)
}
```

Apply the same `claude_binary` → `api_key_env` substitution to: `TestLoad_concurrencyDefaults`, `TestLoad_openAccessWithAllowFromRejected`, `TestLoad_negativeConcurrencyRejected`, `TestLoad_fallbackTiers`, `TestLoad_modelDefaultsToSonnet`, `TestLoad_R1Channel`.

For `TestLoad_modelDefaultsToSonnet`, update the expected default:

```go
	require.Equal(t, "claude-sonnet-4-6", cfg.Model, "unset model defaults to claude-sonnet-4-6")
```

- [ ] **Step 7: Run all config tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/config/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): replace claude_binary with api_key_env, add base_url/token_budget/escalation_model"
```

---

### Task 4: Rewrite FallbackRunner

**Files:**
- Modify: `internal/core/fallback.go`
- Modify: `internal/core/fallback_test.go`

Rewrite FallbackRunner to wrap `APIClient` instances instead of `ClaudeRunner`. The cascade logic stays the same — try primary, on error try each fallback tier.

- [ ] **Step 1: Write failing test for primary success**

Replace `internal/core/fallback_test.go` entirely:

```go
package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockAPI creates an httptest server that returns the given text or error.
func mockAPI(t *testing.T, text string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error":{"type":"api_error","message":"fail"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		})
	}))
}

func TestFallbackRunner_PrimarySuccess(t *testing.T) {
	primary := mockAPI(t, "primary reply", http.StatusOK)
	defer primary.Close()
	fallback := mockAPI(t, "fallback reply", http.StatusOK)
	defer fallback.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "sonnet"),
		[]FallbackTier{{Client: NewAPIClient(fallback.URL, "key", "glm"), Label: "GLM"}},
	)

	result, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "primary reply", result)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestFallbackRunner_PrimarySuccess -v`
Expected: FAIL — type mismatches.

- [ ] **Step 3: Rewrite fallback.go**

Replace `internal/core/fallback.go` entirely:

```go
package core

import (
	"context"
	"fmt"
	"log/slog"
)

// FallbackTier is a labeled APIClient used as a fallback when the primary
// (or a previous tier) fails.
type FallbackTier struct {
	Client *APIClient
	Label  string
}

// FallbackRunner wraps a primary APIClient and zero or more fallback tiers.
// If the primary fails, each fallback tier is tried in order. Fallback
// replies are prefixed with the tier's label (e.g. "[GLM]").
type FallbackRunner struct {
	primary   *APIClient
	fallbacks []FallbackTier
}

// NewFallbackRunner creates a FallbackRunner.
func NewFallbackRunner(primary *APIClient, fallbacks []FallbackTier) *FallbackRunner {
	return &FallbackRunner{
		primary:   primary,
		fallbacks: fallbacks,
	}
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
				"tier", fb.Label,
				"index", i,
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestFallbackRunner_PrimarySuccess -v`
Expected: PASS

- [ ] **Step 5: Add remaining fallback tests**

Append to `internal/core/fallback_test.go`:

```go
func TestFallbackRunner_PrimaryFailsFallsBack(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()
	fallback := mockAPI(t, "fallback reply", http.StatusOK)
	defer fallback.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "sonnet"),
		[]FallbackTier{{Client: NewAPIClient(fallback.URL, "key", "glm"), Label: "GLM"}},
	)

	result, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "[GLM] fallback reply", result)
}

func TestFallbackRunner_AllFail(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()
	fallback := mockAPI(t, "", http.StatusTooManyRequests)
	defer fallback.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "sonnet"),
		[]FallbackTier{{Client: NewAPIClient(fallback.URL, "key", "glm"), Label: "GLM"}},
	)

	_, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "[GLM]")
}

func TestFallbackRunner_NoFallbacksReturnsError(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "sonnet"),
		nil,
	)

	_, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.Error(t, err)
}

func TestFallbackRunner_ThreeTierChain(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()
	glm := mockAPI(t, "", http.StatusInternalServerError)
	defer glm.Close()
	ollama := mockAPI(t, "local reply", http.StatusOK)
	defer ollama.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "sonnet"),
		[]FallbackTier{
			{Client: NewAPIClient(glm.URL, "key", "glm"), Label: "GLM"},
			{Client: NewAPIClient(ollama.URL, "key", "gemma"), Label: "Ollama"},
		},
	)

	result, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "[Ollama] local reply", result)
}
```

- [ ] **Step 6: Run all fallback tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestFallbackRunner -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/core/fallback.go internal/core/fallback_test.go
git commit -m "refactor(core): rewrite FallbackRunner to use APIClient instead of ClaudeRunner"
```

---

### Task 5: Rewrite Dispatcher

**Files:**
- Modify: `internal/core/dispatcher.go`
- Modify: `internal/core/dispatcher_test.go`

Integrate History, model escalation, remove session logic. The Dispatcher no longer needs a SessionStore.

- [ ] **Step 1: Rewrite Dispatcher struct and constructor**

Replace the Dispatcher in `internal/core/dispatcher.go`:

```go
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

// Dispatcher is the shared core that all clients route turns through.
// It looks up conversation history, loads memory, invokes the API,
// handles model escalation, and returns a Reply.
type Dispatcher struct {
	cfg     *config.Config
	access  *Access
	history *History
	memory  *memory.Loader
	claude  *FallbackRunner
	sem     chan struct{}
}

// NewDispatcher wires together the core pieces.
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
```

- [ ] **Step 2: Implement new Dispatch method with history and escalation**

Replace the `Dispatch` method:

```go
// opusPrefix is the trigger for model escalation.
const opusPrefix = "/opus "

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

	// Load memory.
	mem, err := d.memory.Load(turn.Channel)
	if err != nil {
		slog.Warn("memory load failed; continuing without",
			"channel", turn.Channel, "error", err)
		mem = ""
	}

	// Load system prompt file.
	var sysPrompt string
	if path := d.cfg.Channels[turn.Channel].SystemPromptFile; path != "" {
		sysPrompt = d.loadSystemPromptFile(turn.Channel, path)
	}
	combined := combineSystemPrompt(sysPrompt, mem)

	// Load conversation history.
	history, err := d.history.Load()
	if err != nil {
		slog.Warn("history load failed; continuing with empty", "error", err)
		history = nil
	}

	// Check for model escalation.
	message := turn.Message
	model := ""
	if strings.HasPrefix(message, opusPrefix) {
		message = strings.TrimPrefix(message, opusPrefix)
		model = d.cfg.EscalationModel
		slog.Info("model escalation triggered", "channel", turn.Channel, "model", model)
	}

	// Inject current time and source channel.
	now := time.Now().In(mustLoadLA())
	dated := fmt.Sprintf("[Current time: %s | Source: %s]\n\n%s",
		now.Format("Monday, January 2, 2006 3:04 PM"), turn.Channel, message)

	// Build messages: history + new user message.
	msgs := make([]Message, len(history), len(history)+1)
	copy(msgs, history)
	msgs = append(msgs, Message{Role: "user", Content: dated})

	// Call API.
	slog.Info("dispatch: calling API", "channel", turn.Channel, "history_len", len(history))
	text, err := d.claude.Run(ctx, SendArgs{
		System:   combined,
		Messages: msgs,
		Model:    model,
	})
	if err != nil {
		return &Reply{Text: fmt.Sprintf("Error: %s", truncate(err.Error(), 200))}, nil
	}

	// Update history.
	history = d.history.Append(history, dated, text)
	history = d.history.Prune(history)
	if saveErr := d.history.Save(history); saveErr != nil {
		slog.Error("history save failed", "error", saveErr)
	}

	return &Reply{Text: text}, nil
}
```

- [ ] **Step 3: Keep the helper functions**

Keep `maxSystemPromptSize`, `loadSystemPromptFile`, `combineSystemPrompt`, and `mustLoadLA` — they are unchanged. The `mustLoadLA` function currently lives in `claude.go` and will need to move. Add it to `dispatcher.go` if it's not already there:

```go
// mustLoadLA returns America/Los_Angeles, falling back to UTC if tzdata
// is missing.
func mustLoadLA() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.UTC
	}
	return loc
}
```

Also move the `truncate` function from `claude.go` to `dispatcher.go`:

```go
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
```

- [ ] **Step 4: Write dispatcher tests**

Replace `internal/core/dispatcher_test.go` entirely:

```go
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

func newMockDispatcher(t *testing.T, reply string, statusCode int) (*Dispatcher, string, *httptest.Server) {
	t.Helper()
	server := mockAPI(t, reply, statusCode)

	stateDir := t.TempDir()
	memDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:       "ANTHROPIC_API_KEY",
		BaseURL:         server.URL,
		StateDir:        stateDir,
		Concurrency:     2,
		Model:           "claude-sonnet-4-6",
		EscalationModel: "claude-opus-4-6",
		TokenBudget:     80000,
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}

	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	client := NewAPIClient(server.URL, "test-key", cfg.Model)
	fb := NewFallbackRunner(client, nil)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(memDir), fb)

	return d, memDir, server
}

func TestDispatcher_AllowedTurnReturnsReply(t *testing.T) {
	d, _, server := newMockDispatcher(t, "hi alice", http.StatusOK)
	defer server.Close()

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hello", ReceivedAt: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, "hi alice", reply.Text)
}

func TestDispatcher_DeniedUserReturnsError(t *testing.T) {
	d, _, server := newMockDispatcher(t, "should not run", http.StatusOK)
	defer server.Close()

	_, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "mallory", Message: "hi",
	})
	require.ErrorIs(t, err, ErrAccessDenied)
}

func TestDispatcher_HistorySavedBetweenTurns(t *testing.T) {
	d, _, server := newMockDispatcher(t, "reply", http.StatusOK)
	defer server.Close()

	// First turn.
	_, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "first", ReceivedAt: time.Now(),
	})
	require.NoError(t, err)

	// History should now have 2 messages (user + assistant).
	history, err := d.history.Load()
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, "user", history[0].Role)
	require.Equal(t, "assistant", history[1].Role)
}

func TestDispatcher_ModelEscalation(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel = body["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "deep thought"},
			},
		})
	}))
	defer server.Close()

	stateDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:       "ANTHROPIC_API_KEY",
		BaseURL:         server.URL,
		StateDir:        stateDir,
		Concurrency:     2,
		Model:           "claude-sonnet-4-6",
		EscalationModel: "claude-opus-4-6",
		TokenBudget:     80000,
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}

	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	client := NewAPIClient(server.URL, "test-key", cfg.Model)
	fb := NewFallbackRunner(client, nil)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(t.TempDir()), fb)

	_, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "/opus think about this deeply",
		ReceivedAt: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", receivedModel)
}

func TestDispatcher_MemoryInjectedIntoSystemPrompt(t *testing.T) {
	d, memDir, server := newMockDispatcher(t, "ok", http.StatusOK)
	defer server.Close()

	chanDir := filepath.Join(memDir, "telegram")
	require.NoError(t, os.MkdirAll(chanDir, 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(chanDir, "memory.md"),
		[]byte("Leo likes tea."),
		0600,
	))

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi", ReceivedAt: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, "ok", reply.Text)
}

func TestDispatcher_CombineSystemPrompt_Unit(t *testing.T) {
	require.Equal(t, "sys\n\nmem", combineSystemPrompt("sys", "mem"))
	require.Equal(t, "sys", combineSystemPrompt("sys", ""))
	require.Equal(t, "mem", combineSystemPrompt("", "mem"))
	require.Equal(t, "", combineSystemPrompt("", ""))
}

func TestDispatcher_ConcurrencyCapSerializes(t *testing.T) {
	// Use a slow mock server to observe serialization.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "ok"},
			},
		})
	}))
	defer server.Close()

	stateDir := t.TempDir()
	cfg := &config.Config{
		APIKeyEnv:       "ANTHROPIC_API_KEY",
		BaseURL:         server.URL,
		StateDir:        stateDir,
		Concurrency:     1,
		Model:           "claude-sonnet-4-6",
		EscalationModel: "claude-opus-4-6",
		TokenBudget:     80000,
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"alice"}},
		},
	}

	history := NewHistory(filepath.Join(stateDir, "history.json"), cfg.TokenBudget)
	client := NewAPIClient(server.URL, "test-key", cfg.Model)
	fb := NewFallbackRunner(client, nil)
	d := NewDispatcher(cfg, NewAccess(cfg), history, memory.NewLoader(t.TempDir()), fb)

	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Dispatch(context.Background(), Turn{
				Channel: "telegram", UserID: "alice", Message: "hi", ReceivedAt: time.Now(),
			})
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, 400*time.Millisecond, "concurrency=1 should serialize calls")
}

func TestDispatcher_APIErrorReturnsErrorText(t *testing.T) {
	d, _, server := newMockDispatcher(t, "", http.StatusInternalServerError)
	defer server.Close()

	reply, err := d.Dispatch(context.Background(), Turn{
		Channel: "telegram", UserID: "alice", Message: "hi", ReceivedAt: time.Now(),
	})
	require.NoError(t, err, "API errors are returned as reply text, not Go errors")
	require.Contains(t, reply.Text, "Error")
}
```

- [ ] **Step 5: Run all dispatcher tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestDispatcher -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/core/dispatcher.go internal/core/dispatcher_test.go
git commit -m "refactor(core): rewrite Dispatcher for direct API with history and model escalation"
```

---

### Task 6: Wire main.go

**Files:**
- Modify: `cmd/obi-wan-core/main.go`

Replace the `buildDispatcherWithConfig` function to wire APIClient, History, and remove SessionStore/ClaudeRunner.

- [ ] **Step 1: Rewrite buildDispatcherWithConfig**

Replace the `buildDispatcherWithConfig` function in `cmd/obi-wan-core/main.go`:

```go
func buildDispatcherWithConfig(cfgPath string) (*core.Dispatcher, *config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	// Resolve primary API key.
	apiKey := os.Getenv(cfg.APIKeyEnv)
	if apiKey == "" {
		return nil, nil, fmt.Errorf("%s is empty", cfg.APIKeyEnv)
	}

	// History: unified file shared across all channels.
	historyPath := filepath.Join(cfg.StateDir, "history.json")
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create state dir: %w", err)
	}
	history := core.NewHistory(historyPath, cfg.TokenBudget)

	// Memory lives under ~/.claude/channels by convention.
	memRoot := expandHome("~/.claude/channels")
	mem := memory.NewLoader(memRoot)

	// Primary API client.
	primary := core.NewAPIClient(cfg.BaseURL, apiKey, cfg.Model)

	// Fallback tiers.
	var tiers []core.FallbackTier
	if cfg.Fallback.Enabled {
		for _, t := range cfg.Fallback.Tiers {
			tierAPIKey := ""
			if t.APIKeyEnv != "" {
				tierAPIKey = os.Getenv(t.APIKeyEnv)
				if tierAPIKey == "" {
					slog.Warn("fallback tier enabled but API key env var is empty",
						"env", t.APIKeyEnv,
						"label", t.Label,
					)
					continue
				}
			}
			if t.AuthTokenEnv != "" {
				authToken := os.Getenv(t.AuthTokenEnv)
				if authToken != "" {
					tierAPIKey = authToken
				}
			}
			client := core.NewAPIClient(t.BaseURL, tierAPIKey, t.Model)
			tiers = append(tiers, core.FallbackTier{
				Client: client,
				Label:  t.Label,
			})
			slog.Info("fallback tier configured",
				"label", t.Label,
				"base_url", t.BaseURL,
				"model", t.Model,
			)
		}
	}

	fb := core.NewFallbackRunner(primary, tiers)

	return core.NewDispatcher(cfg, core.NewAccess(cfg), history, mem, fb), cfg, nil
}
```

- [ ] **Step 2: Remove old imports**

Remove the `SessionStore`-related import. The imports in main.go should be:

```go
import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/leolaporte/obi-wan-core/internal/clients/r1"
	"github.com/leolaporte/obi-wan-core/internal/clients/telegram"
	"github.com/leolaporte/obi-wan-core/internal/clients/watch"
	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/leolaporte/obi-wan-core/internal/core"
	"github.com/leolaporte/obi-wan-core/internal/memory"
)
```

(Same as before, minus nothing — all these are still used.)

- [ ] **Step 3: Update config.yaml.example**

Replace `config.yaml.example`:

```yaml
# obi-wan-core configuration
# Copy to ~/.config/obi-wan-core/config.yaml and edit.

api_key_env: ANTHROPIC_API_KEY
base_url: https://api.anthropic.com
state_dir: /home/leo/.local/state/obi-wan-core
model: claude-sonnet-4-6
escalation_model: claude-opus-4-6
token_budget: 80000

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

channels:
  telegram:
    enabled: true
    allow_from:
      - "YOUR_TELEGRAM_USER_ID"
  watch:
    enabled: true
    webhook_key_env: OBI_WAN_CORE_WEBHOOK_KEY
  r1:
    enabled: false
```

- [ ] **Step 4: Build and verify**

Run: `cd /home/leo/Projects/obi-wan-core && go build ./cmd/obi-wan-core/`
Expected: Build succeeds with no errors.

- [ ] **Step 5: Run all tests (excluding pre-existing R1 failures)**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ ./internal/config/ ./internal/clients/watch/ ./internal/clients/telegram/ ./cmd/... -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add cmd/obi-wan-core/main.go config.yaml.example
git commit -m "refactor(main): wire APIClient and History, remove ClaudeRunner and SessionStore"
```

---

### Task 7: Delete Old Code

**Files:**
- Delete: `internal/core/claude.go`
- Delete: `internal/core/claude_test.go`
- Delete: `internal/core/session.go`
- Delete: `internal/core/session_test.go`

- [ ] **Step 1: Delete the files**

```bash
cd /home/leo/Projects/obi-wan-core
rm internal/core/claude.go internal/core/claude_test.go
rm internal/core/session.go internal/core/session_test.go
```

- [ ] **Step 2: Verify build**

Run: `cd /home/leo/Projects/obi-wan-core && go build ./cmd/obi-wan-core/`
Expected: Build succeeds. If there are compile errors from stale references, fix them — `truncate` and `mustLoadLA` should already be in `dispatcher.go` from Task 5.

- [ ] **Step 3: Run all tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ ./internal/config/ ./internal/clients/watch/ ./internal/clients/telegram/ ./cmd/... -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add -A internal/core/claude.go internal/core/claude_test.go internal/core/session.go internal/core/session_test.go
git commit -m "chore(core): remove ClaudeRunner and SessionStore (replaced by APIClient and History)"
```

---

### Task 8: Build Binary and Update Deployment

**Files:**
- Build binary: `obi-wan-core` → `~/.local/bin/obi-wan-core`

- [ ] **Step 1: Build release binary**

```bash
cd /home/leo/Projects/obi-wan-core
go build -o obi-wan-core ./cmd/obi-wan-core/
cp obi-wan-core ~/.local/bin/obi-wan-core
```

- [ ] **Step 2: Verify binary runs**

```bash
~/.local/bin/obi-wan-core
```
Expected: `usage: obi-wan-core <serve|dispatch> [flags]`

- [ ] **Step 3: Remind Leo to update real config**

Print a message reminding Leo to update `~/.config/obi-wan-core/config.yaml`:
- Replace `claude_binary: ...` with `api_key_env: ANTHROPIC_API_KEY`
- Add `base_url: https://api.anthropic.com` (or omit for default)
- Change `model: sonnet` to `model: claude-sonnet-4-6`
- Add `escalation_model: claude-opus-4-6`
- Add `token_budget: 80000`
- Change any `auth_token: ollama` to `auth_token_env: OLLAMA_AUTH_TOKEN`
- Make sure `ANTHROPIC_API_KEY` is available in the systemd unit's environment

- [ ] **Step 4: Commit final state**

```bash
cd /home/leo/Projects/obi-wan-core
git add -A
git commit -m "chore: build and deploy direct API migration"
```

---

### Post-Implementation Checklist

- [ ] All tests pass (excluding pre-existing R1 failures)
- [ ] Binary builds and runs
- [ ] Config example updated
- [ ] Old subprocess code deleted
- [ ] Leo has updated real config.yaml
- [ ] Leo has restarted `obi-wan-core.service`
- [ ] Live test: send a Telegram message and verify response
- [ ] Live test: send `/opus test` and verify escalation works
- [ ] Verify history.json is created and growing
