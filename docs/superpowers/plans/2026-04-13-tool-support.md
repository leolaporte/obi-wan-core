# Tool Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add native tool execution to the direct API path — Obsidian file ops, Fastmail JMAP, and background `claude -p` spawning — so Watch/Telegram/R1 can log meals, create events, and trigger heavy tasks.

**Architecture:** A `ToolRegistry` maps tool names to handler functions. The `APIClient.Send()` method gains a tool loop: include tool schemas in every request, execute tool calls locally when Claude returns `tool_use`, send results back, repeat until text response. Tools live in `internal/tools/` with one file per category.

**Tech Stack:** Go stdlib (`os`, `net/http`, `os/exec`, `encoding/json`), Fastmail JMAP via HTTP, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-13-tool-support-design.md`

---

### File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/tools/registry.go` | ToolRegistry: schema list, execute dispatch, Tool interface |
| Create | `internal/tools/registry_test.go` | Registry unit tests |
| Create | `internal/tools/obsidian.go` | Obsidian read/patch/write handlers |
| Create | `internal/tools/obsidian_test.go` | Obsidian tool tests (temp dir fixtures) |
| Create | `internal/tools/fastmail.go` | Fastmail JMAP handlers (create event, create/search contacts) |
| Create | `internal/tools/fastmail_test.go` | Fastmail tests (httptest mock JMAP) |
| Create | `internal/tools/claude.go` | spawn_claude_code handler |
| Create | `internal/tools/claude_test.go` | Spawn tool tests |
| Modify | `internal/core/api.go` | Tool loop in Send(), tool schemas in apiRequest |
| Modify | `internal/core/api_test.go` | Test tool loop with mock tool_use responses |
| Modify | `internal/config/config.go` | Add VaultRoot, FastmailTokenEnv, FastmailUser, FastmailPasswordEnv, ClaudeBinary |
| Modify | `internal/config/config_test.go` | Test new config fields |
| Modify | `cmd/obi-wan-core/main.go` | Create ToolRegistry, pass to APIClient |

---

### Task 1: ToolRegistry

**Files:**
- Create: `internal/tools/registry.go`
- Create: `internal/tools/registry_test.go`

The registry is the central dispatch — maps tool names to handlers and holds the schema list for the API request.

- [ ] **Step 1: Write failing test for registry Execute**

Create `internal/tools/registry_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry_Execute_CallsHandler(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name:        "test_echo",
		Description: "Echoes input",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		Handler: func(ctx context.Context, input json.RawMessage) (string, error) {
			var args struct{ Text string `json:"text"` }
			json.Unmarshal(input, &args)
			return "echo: " + args.Text, nil
		},
	})

	result, err := r.Execute(context.Background(), "test_echo", json.RawMessage(`{"text":"hello"}`))
	require.NoError(t, err)
	require.Equal(t, "echo: hello", result)
}

func TestRegistry_Execute_UnknownToolReturnsError(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "nonexistent", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
}

func TestRegistry_Schemas_ReturnsAllRegistered(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name:        "tool_a",
		Description: "A",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler:     func(ctx context.Context, input json.RawMessage) (string, error) { return "", nil },
	})
	r.Register(Tool{
		Name:        "tool_b",
		Description: "B",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler:     func(ctx context.Context, input json.RawMessage) (string, error) { return "", nil },
	})

	schemas := r.Schemas()
	require.Len(t, schemas, 2)
	require.Equal(t, "tool_a", schemas[0].Name)
	require.Equal(t, "tool_b", schemas[1].Name)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestRegistry -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement ToolRegistry**

Create `internal/tools/registry.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// HandlerFunc is the function signature for tool execution.
type HandlerFunc func(ctx context.Context, input json.RawMessage) (string, error)

// Tool defines a single tool with its API schema and local handler.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Handler     HandlerFunc     `json:"-"`
}

// ToolSchema is the API-facing schema (no handler) sent in the request.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Registry maps tool names to handlers and provides schemas for the API.
type Registry struct {
	tools []Tool
	index map[string]int // name -> index in tools slice
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		index: make(map[string]int),
	}
}

// Register adds a tool. Panics if name is duplicate.
func (r *Registry) Register(t Tool) {
	if _, exists := r.index[t.Name]; exists {
		panic(fmt.Sprintf("duplicate tool: %s", t.Name))
	}
	r.index[t.Name] = len(r.tools)
	r.tools = append(r.tools, t)
}

// Execute runs the named tool's handler. Returns an error if the tool
// is not registered.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	idx, ok := r.index[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return r.tools[idx].Handler(ctx, input)
}

// Schemas returns the API-facing schemas for all registered tools.
func (r *Registry) Schemas() []ToolSchema {
	schemas := make([]ToolSchema, len(r.tools))
	for i, t := range r.tools {
		schemas[i] = ToolSchema{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return schemas
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestRegistry -v`
Expected: All 3 PASS

- [ ] **Step 5: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/tools/registry.go internal/tools/registry_test.go
git commit -m "feat(tools): add ToolRegistry with schema list and execute dispatch"
```

---

### Task 2: Obsidian Tools

**Files:**
- Create: `internal/tools/obsidian.go`
- Create: `internal/tools/obsidian_test.go`

Three tools — read, patch, write — all operating on markdown files under a vault root.

- [ ] **Step 1: Write failing test for obsidian_read_note**

Create `internal/tools/obsidian_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObsidianRead_Success(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, "Daily Notes", "2026", "04")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "2026-04-13.md"),
		[]byte("# Today\nSome content"),
		0600,
	))

	handler := ObsidianReadHandler(vault)
	result, err := handler(context.Background(), json.RawMessage(
		`{"path":"Daily Notes/2026/04/2026-04-13.md"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "# Today\nSome content", result)
}

func TestObsidianRead_NotFound(t *testing.T) {
	vault := t.TempDir()
	handler := ObsidianReadHandler(vault)
	result, err := handler(context.Background(), json.RawMessage(
		`{"path":"nonexistent.md"}`,
	))
	require.NoError(t, err, "not found is a tool result, not a Go error")
	require.Contains(t, result, "not found")
}

func TestObsidianRead_PathTraversal(t *testing.T) {
	vault := t.TempDir()
	handler := ObsidianReadHandler(vault)
	_, err := handler(context.Background(), json.RawMessage(
		`{"path":"../../etc/passwd"}`,
	))
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside vault")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestObsidianRead -v`
Expected: FAIL — `ObsidianReadHandler` undefined.

- [ ] **Step 3: Implement obsidian.go with read handler**

Create `internal/tools/obsidian.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// obsidianReadInput is the JSON input for obsidian_read_note.
type obsidianReadInput struct {
	Path string `json:"path"`
}

// obsidianPatchInput is the JSON input for obsidian_patch_note.
type obsidianPatchInput struct {
	Path     string `json:"path"`
	Heading  string `json:"heading"`
	Content  string `json:"content"`
	Position string `json:"position"` // "append" or "replace"
}

// obsidianWriteInput is the JSON input for obsidian_write_note.
type obsidianWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// safePath resolves path relative to vaultRoot and rejects traversal.
func safePath(vaultRoot, relPath string) (string, error) {
	full := filepath.Join(vaultRoot, relPath)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	vaultAbs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", fmt.Errorf("resolve vault root: %w", err)
	}
	if !strings.HasPrefix(abs, vaultAbs+string(filepath.Separator)) && abs != vaultAbs {
		return "", fmt.Errorf("path outside vault: %s", relPath)
	}
	return abs, nil
}

// ObsidianReadHandler returns a handler for obsidian_read_note.
func ObsidianReadHandler(vaultRoot string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args obsidianReadInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}

		path, err := safePath(vaultRoot, args.Path)
		if err != nil {
			return "", err
		}

		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return fmt.Sprintf("File not found: %s", args.Path), nil
		}
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		return string(data), nil
	}
}

// ObsidianWriteHandler returns a handler for obsidian_write_note.
func ObsidianWriteHandler(vaultRoot string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args obsidianWriteInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}

		path, err := safePath(vaultRoot, args.Path)
		if err != nil {
			return "", err
		}

		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", fmt.Errorf("create dirs: %w", err)
		}
		if err := os.WriteFile(path, []byte(args.Content), 0600); err != nil {
			return "", fmt.Errorf("write file: %w", err)
		}
		return "OK", nil
	}
}

// ObsidianPatchHandler returns a handler for obsidian_patch_note.
// It inserts content under a heading in an existing file.
func ObsidianPatchHandler(vaultRoot string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args obsidianPatchInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		if args.Position == "" {
			args.Position = "append"
		}

		path, err := safePath(vaultRoot, args.Path)
		if err != nil {
			return "", err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}

		lines := strings.Split(string(data), "\n")
		headingIdx := -1
		for i, line := range lines {
			if strings.TrimSpace(line) == args.Heading {
				headingIdx = i
				break
			}
		}
		if headingIdx == -1 {
			return fmt.Sprintf("Heading not found: %s", args.Heading), nil
		}

		// Find the end of this section (next heading of same or higher level, or EOF).
		headingLevel := countLeadingHashes(args.Heading)
		endIdx := len(lines)
		for i := headingIdx + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "#") {
				level := countLeadingHashes(trimmed)
				if level <= headingLevel {
					endIdx = i
					break
				}
			}
		}

		contentLines := strings.Split(args.Content, "\n")

		var result []string
		if args.Position == "replace" {
			result = append(result, lines[:headingIdx+1]...)
			result = append(result, contentLines...)
			result = append(result, lines[endIdx:]...)
		} else {
			// append: insert before endIdx
			result = append(result, lines[:endIdx]...)
			result = append(result, contentLines...)
			result = append(result, lines[endIdx:]...)
		}

		if err := os.WriteFile(path, []byte(strings.Join(result, "\n")), 0600); err != nil {
			return "", fmt.Errorf("write file: %w", err)
		}
		return "OK", nil
	}
}

// countLeadingHashes counts the number of leading '#' characters.
func countLeadingHashes(s string) int {
	s = strings.TrimSpace(s)
	count := 0
	for _, ch := range s {
		if ch == '#' {
			count++
		} else {
			break
		}
	}
	return count
}

// RegisterObsidianTools registers all Obsidian tools in the registry.
func RegisterObsidianTools(r *Registry, vaultRoot string) {
	r.Register(Tool{
		Name:        "obsidian_read_note",
		Description: "Read a note from the Obsidian vault. Path is relative to vault root.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Relative path to the note, e.g. Daily Notes/2026/04/2026-04-13.md"}
			},
			"required": ["path"]
		}`),
		Handler: ObsidianReadHandler(vaultRoot),
	})
	r.Register(Tool{
		Name:        "obsidian_patch_note",
		Description: "Insert content under a heading in an existing Obsidian note. Used for meals, exercise, voice notes, tasks, shopping lists.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Relative path to the note"},
				"heading": {"type": "string", "description": "Exact heading text to find, e.g. #### Meals"},
				"content": {"type": "string", "description": "Content to insert"},
				"position": {"type": "string", "enum": ["append", "replace"], "description": "append after existing content or replace section content"}
			},
			"required": ["path", "heading", "content"]
		}`),
		Handler: ObsidianPatchHandler(vaultRoot),
	})
	r.Register(Tool{
		Name:        "obsidian_write_note",
		Description: "Create or overwrite a note in the Obsidian vault. Creates parent directories if needed.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Relative path to the note"},
				"content": {"type": "string", "description": "Full content of the note"}
			},
			"required": ["path", "content"]
		}`),
		Handler: ObsidianWriteHandler(vaultRoot),
	})
}
```

- [ ] **Step 4: Run read tests to verify they pass**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestObsidianRead -v`
Expected: All 3 PASS

- [ ] **Step 5: Write tests for obsidian_write_note**

Append to `internal/tools/obsidian_test.go`:

```go
func TestObsidianWrite_CreatesFileAndDirs(t *testing.T) {
	vault := t.TempDir()
	handler := ObsidianWriteHandler(vault)

	result, err := handler(context.Background(), json.RawMessage(
		`{"path":"new/sub/note.md","content":"# Hello\nWorld"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "OK", result)

	data, err := os.ReadFile(filepath.Join(vault, "new", "sub", "note.md"))
	require.NoError(t, err)
	require.Equal(t, "# Hello\nWorld", string(data))
}

func TestObsidianWrite_PathTraversal(t *testing.T) {
	vault := t.TempDir()
	handler := ObsidianWriteHandler(vault)
	_, err := handler(context.Background(), json.RawMessage(
		`{"path":"../../evil.md","content":"bad"}`,
	))
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside vault")
}
```

- [ ] **Step 6: Run write tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestObsidianWrite -v`
Expected: All 2 PASS

- [ ] **Step 7: Write tests for obsidian_patch_note**

Append to `internal/tools/obsidian_test.go`:

```go
func TestObsidianPatch_AppendUnderHeading(t *testing.T) {
	vault := t.TempDir()
	note := "# Daily\n#### Meals\n| existing |\n#### Exercise\nRun\n"
	require.NoError(t, os.WriteFile(filepath.Join(vault, "note.md"), []byte(note), 0600))

	handler := ObsidianPatchHandler(vault)
	result, err := handler(context.Background(), json.RawMessage(
		`{"path":"note.md","heading":"#### Meals","content":"| new row |"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "OK", result)

	data, err := os.ReadFile(filepath.Join(vault, "note.md"))
	require.NoError(t, err)
	content := string(data)
	require.Contains(t, content, "| existing |")
	require.Contains(t, content, "| new row |")
	// New row should be before #### Exercise
	mealsIdx := strings.Index(content, "| new row |")
	exerciseIdx := strings.Index(content, "#### Exercise")
	require.Less(t, mealsIdx, exerciseIdx, "new content should be before next heading")
}

func TestObsidianPatch_ReplaceUnderHeading(t *testing.T) {
	vault := t.TempDir()
	note := "# Daily\n#### Meals\nold content\n#### Exercise\nRun\n"
	require.NoError(t, os.WriteFile(filepath.Join(vault, "note.md"), []byte(note), 0600))

	handler := ObsidianPatchHandler(vault)
	result, err := handler(context.Background(), json.RawMessage(
		`{"path":"note.md","heading":"#### Meals","content":"new content","position":"replace"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "OK", result)

	data, err := os.ReadFile(filepath.Join(vault, "note.md"))
	require.NoError(t, err)
	content := string(data)
	require.NotContains(t, content, "old content")
	require.Contains(t, content, "new content")
	require.Contains(t, content, "#### Exercise")
}

func TestObsidianPatch_HeadingNotFound(t *testing.T) {
	vault := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(vault, "note.md"), []byte("# No match"), 0600))

	handler := ObsidianPatchHandler(vault)
	result, err := handler(context.Background(), json.RawMessage(
		`{"path":"note.md","heading":"#### Missing","content":"stuff"}`,
	))
	require.NoError(t, err)
	require.Contains(t, result, "not found")
}
```

Add `"strings"` to the import block in the test file.

- [ ] **Step 8: Run patch tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestObsidianPatch -v`
Expected: All 3 PASS

- [ ] **Step 9: Run all Obsidian tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -v`
Expected: All PASS (registry + obsidian tests)

- [ ] **Step 10: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/tools/obsidian.go internal/tools/obsidian_test.go
git commit -m "feat(tools): add Obsidian read/patch/write tools with path traversal protection"
```

---

### Task 3: Fastmail Tools

**Files:**
- Create: `internal/tools/fastmail.go`
- Create: `internal/tools/fastmail_test.go`

Three tools — create event (CalDAV), create contact (JMAP), search contacts (JMAP). All tested with httptest mock servers.

- [ ] **Step 1: Write failing test for create_calendar_event**

Create `internal/tools/fastmail_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFastmailCreateEvent_Success(t *testing.T) {
	var receivedBody string
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	handler := FastmailCreateEventHandler(srv.URL, "testuser@fastmail.com", "testpass")
	result, err := handler(context.Background(), json.RawMessage(
		`{"title":"Dentist","start":"2026-04-15T14:00:00","duration":"PT1H","calendar":"Personal"}`,
	))
	require.NoError(t, err)
	require.Contains(t, result, "Dentist")
	require.Contains(t, result, "created")
	require.Contains(t, receivedBody, "SUMMARY:Dentist")
	require.Contains(t, receivedBody, "DTSTART:")
	require.True(t, strings.HasPrefix(receivedAuth, "Basic "))
}

func TestFastmailCreateEvent_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	handler := FastmailCreateEventHandler(srv.URL, "user", "pass")
	result, err := handler(context.Background(), json.RawMessage(
		`{"title":"Test","start":"2026-04-15T14:00:00","duration":"PT1H"}`,
	))
	require.NoError(t, err, "server errors are tool results, not Go errors")
	require.Contains(t, result, "error")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestFastmailCreateEvent -v`
Expected: FAIL — `FastmailCreateEventHandler` undefined.

- [ ] **Step 3: Implement fastmail.go with create event handler**

Create `internal/tools/fastmail.go`:

```go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"crypto/rand"
)

// fastmailCreateEventInput is the JSON input for fastmail_create_event.
type fastmailCreateEventInput struct {
	Title    string `json:"title"`
	Start    string `json:"start"`    // ISO 8601: 2026-04-15T14:00:00
	Duration string `json:"duration"` // ISO 8601 duration: PT1H, PT30M
	Location string `json:"location,omitempty"`
	Calendar string `json:"calendar,omitempty"` // defaults to "Personal"
}

// fastmailContactInput is the JSON input for fastmail_create_contact.
type fastmailContactInput struct {
	Name    string `json:"name"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Company string `json:"company,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// fastmailSearchInput is the JSON input for fastmail_search_contacts.
type fastmailSearchInput struct {
	Query string `json:"query"`
}

// FastmailCreateEventHandler returns a handler that creates calendar events via CalDAV.
func FastmailCreateEventHandler(caldavURL, user, password string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args fastmailCreateEventInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}
		if args.Calendar == "" {
			args.Calendar = "Personal"
		}
		if args.Duration == "" {
			args.Duration = "PT1H"
		}

		// Parse start time.
		startTime, err := time.Parse("2006-01-02T15:04:05", args.Start)
		if err != nil {
			return fmt.Sprintf("Invalid start time: %s (expected format: 2026-04-15T14:00:00)", args.Start), nil
		}

		uid := generateUID()
		vcal := buildVCalendar(uid, args.Title, args.Location, startTime, args.Duration)

		// PUT to CalDAV.
		url := fmt.Sprintf("%s/dav/calendars/user/%s/%s/%s.ics",
			strings.TrimRight(caldavURL, "/"), user, args.Calendar, uid)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader([]byte(vcal)))
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}
		req.SetBasicAuth(user, password)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("caldav request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Sprintf("Calendar error (HTTP %d): %s", resp.StatusCode, truncateStr(string(body), 200)), nil
		}

		return fmt.Sprintf("Event created: %s on %s", args.Title, startTime.Format("Monday, January 2 at 3:04 PM")), nil
	}
}

// buildVCalendar creates a minimal iCalendar string.
func buildVCalendar(uid, summary, location string, start time.Time, duration string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//obi-wan-core//EN\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString(fmt.Sprintf("UID:%s\r\n", uid))
	b.WriteString(fmt.Sprintf("DTSTART:%s\r\n", start.Format("20060102T150405")))
	b.WriteString(fmt.Sprintf("DURATION:%s\r\n", duration))
	b.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", summary))
	if location != "" {
		b.WriteString(fmt.Sprintf("LOCATION:%s\r\n", location))
	}
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// FastmailCreateContactHandler returns a handler that creates contacts via JMAP.
func FastmailCreateContactHandler(jmapURL, token string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args fastmailContactInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}

		// Build JMAP request.
		contact := map[string]any{
			"firstName": args.Name,
		}
		if args.Email != "" {
			contact["emails"] = []map[string]any{
				{"type": "personal", "value": args.Email},
			}
		}
		if args.Phone != "" {
			contact["phones"] = []map[string]any{
				{"type": "mobile", "value": args.Phone},
			}
		}
		if args.Company != "" {
			contact["company"] = args.Company
		}
		if args.Notes != "" {
			contact["notes"] = args.Notes
		}

		jmapReq := map[string]any{
			"using":       []string{"urn:ietf:params:jmap:contacts", "https://www.fastmail.com/dev/contacts"},
			"methodCalls": []any{
				[]any{"ContactCard/set", map[string]any{
					"accountId": "primary",
					"create":    map[string]any{"c1": contact},
				}, "0"},
			},
		}

		return doJMAP(ctx, jmapURL, token, jmapReq,
			fmt.Sprintf("Contact created: %s", args.Name))
	}
}

// FastmailSearchContactsHandler returns a handler that searches contacts via JMAP.
func FastmailSearchContactsHandler(jmapURL, token string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args fastmailSearchInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}

		jmapReq := map[string]any{
			"using":       []string{"urn:ietf:params:jmap:contacts", "https://www.fastmail.com/dev/contacts"},
			"methodCalls": []any{
				[]any{"ContactCard/query", map[string]any{
					"accountId": "primary",
					"filter":    map[string]any{"text": args.Query},
					"limit":     10,
				}, "0"},
				[]any{"ContactCard/get", map[string]any{
					"accountId":  "primary",
					"#ids":       map[string]any{"resultOf": "0", "name": "ContactCard/query", "path": "/ids"},
					"properties": []string{"name", "emails", "phones", "company"},
				}, "1"},
			},
		}

		return doJMAP(ctx, jmapURL, token, jmapReq, "")
	}
}

// doJMAP sends a JMAP request and returns the response body or a success message.
func doJMAP(ctx context.Context, jmapURL, token string, body any, successMsg string) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal jmap: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, jmapURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("jmap request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("JMAP error (HTTP %d): %s", resp.StatusCode, truncateStr(string(respBody), 200)), nil
	}

	if successMsg != "" {
		return successMsg, nil
	}
	// Return raw JMAP response for search results — Claude will format it.
	return string(respBody), nil
}

// generateUID creates a unique ID for calendar events.
func generateUID() string {
	return fmt.Sprintf("%s@obi-wan-core", newUUID())
}

// newUUID generates a UUID v4 string. Uses crypto/rand via a simple approach.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = randReader.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// randReader is the random source (overridable in tests).
var randReader io.Reader = cryptoRandReader{}

type cryptoRandReader struct{}

func (cryptoRandReader) Read(b []byte) (int, error) {
	return rand.Read(b)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// RegisterFastmailTools registers all Fastmail tools in the registry.
func RegisterFastmailTools(r *Registry, caldavURL, user, password, jmapURL, token string) {
	r.Register(Tool{
		Name:        "fastmail_create_event",
		Description: "Create a calendar event. Uses Fastmail CalDAV.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {"type": "string", "description": "Event title"},
				"start": {"type": "string", "description": "Start time in ISO 8601 format, e.g. 2026-04-15T14:00:00"},
				"duration": {"type": "string", "description": "Duration in ISO 8601 format, e.g. PT1H for 1 hour, PT30M for 30 minutes"},
				"location": {"type": "string", "description": "Event location"},
				"calendar": {"type": "string", "description": "Calendar name, defaults to Personal"}
			},
			"required": ["title", "start"]
		}`),
		Handler: FastmailCreateEventHandler(caldavURL, user, password),
	})
	r.Register(Tool{
		Name:        "fastmail_create_contact",
		Description: "Create a new contact in Fastmail.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Contact name"},
				"email": {"type": "string", "description": "Email address"},
				"phone": {"type": "string", "description": "Phone number"},
				"company": {"type": "string", "description": "Company name"},
				"notes": {"type": "string", "description": "Additional notes"}
			},
			"required": ["name"]
		}`),
		Handler: FastmailCreateContactHandler(jmapURL, token),
	})
	r.Register(Tool{
		Name:        "fastmail_search_contacts",
		Description: "Search contacts by name. Returns matching contacts with IDs, names, emails.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Search text to match against contact names"}
			},
			"required": ["query"]
		}`),
		Handler: FastmailSearchContactsHandler(jmapURL, token),
	})
}
```

Note: The `rand` import needs `"crypto/rand"`. Add it to the import block.

- [ ] **Step 4: Run create event tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestFastmailCreateEvent -v`
Expected: All PASS

- [ ] **Step 5: Write and run contact tests**

Append to `internal/tools/fastmail_test.go`:

```go
func TestFastmailCreateContact_Success(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"methodResponses":[]}`))
	}))
	defer srv.Close()

	handler := FastmailCreateContactHandler(srv.URL, "test-token")
	result, err := handler(context.Background(), json.RawMessage(
		`{"name":"Jeff Mahan","email":"jeff@example.com","phone":"555-1234"}`,
	))
	require.NoError(t, err)
	require.Contains(t, result, "Jeff Mahan")
	require.Equal(t, "Bearer test-token", receivedAuth)
}

func TestFastmailSearchContacts_ReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"methodResponses":[["ContactCard/get",{"list":[{"name":"Jeff"}]},"1"]]}`))
	}))
	defer srv.Close()

	handler := FastmailSearchContactsHandler(srv.URL, "test-token")
	result, err := handler(context.Background(), json.RawMessage(`{"query":"Jeff"}`))
	require.NoError(t, err)
	require.Contains(t, result, "Jeff")
}
```

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestFastmail -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/tools/fastmail.go internal/tools/fastmail_test.go
git commit -m "feat(tools): add Fastmail CalDAV event and JMAP contact tools"
```

---

### Task 4: Spawn Claude Code Tool

**Files:**
- Create: `internal/tools/claude.go`
- Create: `internal/tools/claude_test.go`

Fire-and-forget background `claude -p` for heavy tasks.

- [ ] **Step 1: Write failing test**

Create `internal/tools/claude_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpawnClaude_FiresProcess(t *testing.T) {
	// Use a mock script instead of real claude binary.
	dir := t.TempDir()
	marker := filepath.Join(dir, "spawned.txt")
	mockBin := filepath.Join(dir, "claude")
	// Script writes a marker file to prove it ran.
	script := "#!/bin/bash\necho \"$@\" > " + marker + "\n"
	require.NoError(t, os.WriteFile(mockBin, []byte(script), 0700))

	handler := SpawnClaudeCodeHandler(mockBin)
	result, err := handler(context.Background(), json.RawMessage(
		`{"task":"Research Jeff Mahan for showprep","skill":"showprep"}`,
	))
	require.NoError(t, err)
	require.Contains(t, result, "spawned")

	// Wait briefly for background process to write marker.
	time.Sleep(500 * time.Millisecond)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Contains(t, string(data), "showprep")
	require.Contains(t, string(data), "Jeff Mahan")
}

func TestSpawnClaude_NoSkill(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "spawned.txt")
	mockBin := filepath.Join(dir, "claude")
	script := "#!/bin/bash\necho \"$@\" > " + marker + "\n"
	require.NoError(t, os.WriteFile(mockBin, []byte(script), 0700))

	handler := SpawnClaudeCodeHandler(mockBin)
	result, err := handler(context.Background(), json.RawMessage(
		`{"task":"Analyze the latest HN discussion about AI"}`,
	))
	require.NoError(t, err)
	require.Contains(t, result, "spawned")

	time.Sleep(500 * time.Millisecond)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Contains(t, string(data), "Analyze")
	require.NotContains(t, string(data), "/")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestSpawnClaude -v`
Expected: FAIL — `SpawnClaudeCodeHandler` undefined.

- [ ] **Step 3: Implement claude.go**

Create `internal/tools/claude.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
)

// spawnClaudeInput is the JSON input for spawn_claude_code.
type spawnClaudeInput struct {
	Task  string `json:"task"`
	Skill string `json:"skill,omitempty"`
}

// SpawnClaudeCodeHandler returns a fire-and-forget handler that spawns
// claude -p in the background with the full Claude Code environment.
func SpawnClaudeCodeHandler(claudeBinary string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args spawnClaudeInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}

		prompt := args.Task
		if args.Skill != "" {
			prompt = fmt.Sprintf("/%s %s", args.Skill, args.Task)
		}

		cmd := exec.Command(claudeBinary, "-p", "--model", "opus", prompt)
		if err := cmd.Start(); err != nil {
			return fmt.Sprintf("Failed to spawn: %s", err), nil
		}

		// Fire and forget — don't wait for completion.
		go func() {
			err := cmd.Wait()
			if err != nil {
				slog.Error("spawned claude process failed",
					"task", args.Task,
					"skill", args.Skill,
					"error", err,
				)
			} else {
				slog.Info("spawned claude process completed",
					"task", args.Task,
					"skill", args.Skill,
				)
			}
		}()

		return fmt.Sprintf("Task spawned: %s", args.Task), nil
	}
}

// RegisterClaudeTools registers the spawn tool in the registry.
func RegisterClaudeTools(r *Registry, claudeBinary string) {
	r.Register(Tool{
		Name:        "spawn_claude_code",
		Description: "Spawn a background Claude Code process for heavy tasks like research, showprep, council, wiki ingest, or code review. The process runs asynchronously with full Claude Code capabilities (skills, MCP servers, tools). Results are written to Obsidian or other destinations by the invoked skill. Use this for anything that needs deep research, multiple tool calls, or long-running work.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {"type": "string", "description": "Description of the task to perform"},
				"skill": {"type": "string", "description": "Optional skill to invoke, e.g. showprep, council, research. Omit for general tasks."}
			},
			"required": ["task"]
		}`),
		Handler: SpawnClaudeCodeHandler(claudeBinary),
	})
}
```

- [ ] **Step 4: Run tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/tools/ -run TestSpawnClaude -v`
Expected: All 2 PASS

- [ ] **Step 5: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/tools/claude.go internal/tools/claude_test.go
git commit -m "feat(tools): add spawn_claude_code fire-and-forget tool"
```

---

### Task 5: APIClient Tool Loop

**Files:**
- Modify: `internal/core/api.go`
- Modify: `internal/core/api_test.go`

Add tool schemas to the request and a loop that handles `tool_use` responses.

- [ ] **Step 1: Write failing test for tool loop**

Append to `internal/core/api_test.go`:

```go
func TestAPIClient_Send_ToolLoop(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		callCount++
		w.Header().Set("Content-Type", "application/json")

		if callCount == 1 {
			// First call: verify tools are in request, return tool_use
			tools := req["tools"]
			require.NotNil(t, tools, "tools should be in request")

			json.NewEncoder(w).Encode(map[string]any{
				"stop_reason": "tool_use",
				"content": []map[string]any{
					{"type": "text", "text": "Let me check that."},
					{"type": "tool_use", "id": "call_1", "name": "test_tool", "input": map[string]any{"key": "val"}},
				},
			})
		} else {
			// Second call: return final text
			json.NewEncoder(w).Encode(map[string]any{
				"stop_reason": "end_turn",
				"content": []map[string]any{
					{"type": "text", "text": "Done! The result was: mock_result"},
				},
			})
		}
	}))
	defer srv.Close()

	// Create a mock tool executor.
	executor := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		require.Equal(t, "test_tool", name)
		return "mock_result", nil
	}

	client := NewAPIClient(srv.URL, "key", "claude-test")
	client.SetToolExecutor(executor)
	client.SetToolSchemas([]json.RawMessage{
		json.RawMessage(`{"name":"test_tool","description":"test","input_schema":{"type":"object"}}`),
	})

	result, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
	require.Equal(t, "Done! The result was: mock_result", result)
	require.Equal(t, 2, callCount, "should have made 2 API calls")
}

func TestAPIClient_Send_NoTools_StillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "Hello!"},
			},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "key", "claude-test")
	// No tools set — should work exactly as before.
	result, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello!", result)
}

func TestAPIClient_Send_ToolLoopMaxIterations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return tool_use — should be stopped by max iterations.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "call_loop", "name": "test_tool", "input": map[string]any{}},
			},
		})
	}))
	defer srv.Close()

	executor := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return "ok", nil
	}

	client := NewAPIClient(srv.URL, "key", "claude-test")
	client.SetToolExecutor(executor)
	client.SetToolSchemas([]json.RawMessage{
		json.RawMessage(`{"name":"test_tool","description":"test","input_schema":{"type":"object"}}`),
	})

	_, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "max tool iterations")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient_Send_Tool -v`
Expected: FAIL — `SetToolExecutor` undefined.

- [ ] **Step 3: Update api.go with tool loop**

The key changes to `internal/core/api.go`:

1. Add `ToolExecutor` type and fields to `APIClient`:

```go
// ToolExecutor is called when the API returns a tool_use block.
type ToolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, error)
```

2. Add `toolExecutor` and `toolSchemas` fields to `APIClient` struct:

```go
type APIClient struct {
	baseURL      string
	apiKey       string
	model        string
	http         *http.Client
	toolExecutor ToolExecutor
	toolSchemas  []json.RawMessage
}
```

3. Add setter methods:

```go
func (c *APIClient) SetToolExecutor(fn ToolExecutor) { c.toolExecutor = fn }
func (c *APIClient) SetToolSchemas(schemas []json.RawMessage) { c.toolSchemas = schemas }
```

4. Update `apiRequest` to include tools:

```go
type apiRequest struct {
	Model     string            `json:"model"`
	System    string            `json:"system,omitempty"`
	Messages  []Message         `json:"messages"`
	MaxTokens int               `json:"max_tokens"`
	Tools     []json.RawMessage `json:"tools,omitempty"`
}
```

5. Update `apiResponse` to handle tool_use blocks:

```go
type apiResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}
```

6. Rewrite `Send` with the tool loop:

```go
const maxToolIterations = 10

func (c *APIClient) Send(ctx context.Context, args SendArgs) (string, error) {
	model := c.model
	if args.Model != "" {
		model = args.Model
	}

	// Build initial messages.
	messages := make([]any, len(args.Messages))
	for i, m := range args.Messages {
		messages[i] = m
	}

	for iteration := 0; ; iteration++ {
		if iteration >= maxToolIterations {
			return "", fmt.Errorf("max tool iterations (%d) exceeded", maxToolIterations)
		}

		reqBody := map[string]any{
			"model":      model,
			"messages":   messages,
			"max_tokens": 4096,
		}
		if args.System != "" {
			reqBody["system"] = args.System
		}
		if len(c.toolSchemas) > 0 {
			reqBody["tools"] = c.toolSchemas
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return "", fmt.Errorf("api: marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("api: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := c.http.Do(req)
		if err != nil {
			return "", fmt.Errorf("api: http request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("api: read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("api: status %d: %s", resp.StatusCode, truncate(string(body), 200))
		}

		var parsed apiResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return "", fmt.Errorf("api: unmarshal response: %w", err)
		}

		if parsed.Error != nil {
			return "", fmt.Errorf("api: %s: %s", parsed.Error.Type, parsed.Error.Message)
		}

		// Check if we got tool_use blocks.
		var toolUses []contentBlock
		var textParts []string
		for _, block := range parsed.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					textParts = append(textParts, block.Text)
				}
			case "tool_use":
				toolUses = append(toolUses, block)
			}
		}

		// No tool use — return text.
		if parsed.StopReason != "tool_use" || len(toolUses) == 0 {
			text := strings.Join(textParts, "\n")
			if text == "" {
				return "", fmt.Errorf("api: no text content in response")
			}
			return text, nil
		}

		// Tool use — execute and continue loop.
		if c.toolExecutor == nil {
			return "", fmt.Errorf("api: tool_use response but no executor configured")
		}

		// Append assistant message (with tool_use blocks) to messages.
		// Use raw parsed content to preserve the full structure.
		var rawContent []map[string]any
		json.Unmarshal(body, &struct {
			Content *[]map[string]any `json:"content"`
		}{&rawContent})

		messages = append(messages, map[string]any{
			"role":    "assistant",
			"content": rawContent,
		})

		// Execute each tool and append results.
		for _, tu := range toolUses {
			result, execErr := c.toolExecutor(ctx, tu.Name, tu.Input)
			if execErr != nil {
				result = fmt.Sprintf("Tool error: %s", execErr.Error())
			}
			messages = append(messages, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": tu.ID,
						"content":     result,
					},
				},
			})
		}
	}
}
```

Note: Add `"strings"` to the import block. Remove the old `apiRequest` and `apiResponse` structs (replaced by the new ones above). The `Message` type is still used for the initial messages but the loop uses `[]any` for the messages array since tool results have a different shape.

- [ ] **Step 4: Run tool loop tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -run TestAPIClient -v`
Expected: All PASS (new tool tests + existing tests still work)

- [ ] **Step 5: Run full core tests for regressions**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/core/api.go internal/core/api_test.go
git commit -m "feat(core): add tool loop to APIClient with max iteration safety"
```

---

### Task 6: Config Changes

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

Add new config fields for vault root, Fastmail credentials, and claude binary.

- [ ] **Step 1: Write failing test for new config fields**

Add to `internal/config/config_test.go`:

```go
func TestLoad_toolConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
api_key_env: ANTHROPIC_API_KEY
state_dir: /tmp/obi-wan-core-test
vault_root: ~/Obsidian/lgl
fastmail_token_env: FASTMAIL_API_TOKEN
fastmail_user: leo@fastmail.com
fastmail_password_env: FASTMAIL_PASSWORD
claude_binary: /home/leo/.local/bin/claude
channels:
  telegram:
    enabled: true
    allow_from: ["1"]
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "~/Obsidian/lgl", cfg.VaultRoot)
	require.Equal(t, "FASTMAIL_API_TOKEN", cfg.FastmailTokenEnv)
	require.Equal(t, "leo@fastmail.com", cfg.FastmailUser)
	require.Equal(t, "FASTMAIL_PASSWORD", cfg.FastmailPasswordEnv)
	require.Equal(t, "/home/leo/.local/bin/claude", cfg.ClaudeBinary)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/config/ -run TestLoad_toolConfig -v`
Expected: FAIL — `cfg.VaultRoot` undefined.

- [ ] **Step 3: Add fields to Config struct**

Add after the `Channels` field in `internal/config/config.go`:

```go
	// Tool support
	VaultRoot          string `yaml:"vault_root"`
	FastmailTokenEnv   string `yaml:"fastmail_token_env"`
	FastmailUser       string `yaml:"fastmail_user"`
	FastmailPasswordEnv string `yaml:"fastmail_password_env"`
	ClaudeBinary       string `yaml:"claude_binary"`
```

No validation needed — all are optional (tools degrade gracefully if not configured).

- [ ] **Step 4: Run test**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/config/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add vault_root, fastmail, and claude_binary fields for tool support"
```

---

### Task 7: Wire Everything in main.go

**Files:**
- Modify: `cmd/obi-wan-core/main.go`

Create the ToolRegistry, register all tools, and pass it to the APIClient.

- [ ] **Step 1: Update buildDispatcherWithConfig**

In `cmd/obi-wan-core/main.go`, after creating the primary APIClient and before creating the FallbackRunner, add tool registration:

```go
	// Tool registry.
	registry := tools.NewRegistry()

	// Obsidian tools (if vault_root configured).
	if cfg.VaultRoot != "" {
		vaultRoot := expandHome(cfg.VaultRoot)
		tools.RegisterObsidianTools(registry, vaultRoot)
		slog.Info("obsidian tools registered", "vault", vaultRoot)
	}

	// Fastmail tools (if credentials configured).
	if cfg.FastmailTokenEnv != "" || cfg.FastmailUser != "" {
		fmToken := os.Getenv(cfg.FastmailTokenEnv)
		fmPassword := ""
		if cfg.FastmailPasswordEnv != "" {
			fmPassword = os.Getenv(cfg.FastmailPasswordEnv)
		}
		tools.RegisterFastmailTools(registry,
			"https://caldav.fastmail.com",
			cfg.FastmailUser, fmPassword,
			"https://api.fastmail.com/jmap/api/", fmToken,
		)
		slog.Info("fastmail tools registered")
	}

	// Spawn claude tool (if binary configured or found in PATH).
	claudeBin := cfg.ClaudeBinary
	if claudeBin == "" {
		if found, err := exec.LookPath("claude"); err == nil {
			claudeBin = found
		}
	} else {
		claudeBin = expandHome(claudeBin)
	}
	if claudeBin != "" {
		tools.RegisterClaudeTools(registry, claudeBin)
		slog.Info("spawn_claude_code tool registered", "binary", claudeBin)
	}

	// Wire tools into API client.
	schemas := registry.Schemas()
	if len(schemas) > 0 {
		rawSchemas := make([]json.RawMessage, len(schemas))
		for i, s := range schemas {
			rawSchemas[i], _ = json.Marshal(s)
		}
		primary.SetToolSchemas(rawSchemas)
		primary.SetToolExecutor(registry.Execute)
	}
```

Add imports for `"os/exec"`, `"encoding/json"`, and `"github.com/leolaporte/obi-wan-core/internal/tools"`.

Note: Only the primary client gets tools. Fallback tiers are for when the primary API is down — they don't need tool support (and the fallback models may not support tools well).

- [ ] **Step 2: Update config.yaml.example**

Add the new fields to the example config:

```yaml
# Tool support (optional — tools degrade gracefully if not configured)
vault_root: ~/Obsidian/lgl
fastmail_token_env: FASTMAIL_API_TOKEN
fastmail_user: your_email@fastmail.com
fastmail_password_env: FASTMAIL_PASSWORD
claude_binary: claude
```

- [ ] **Step 3: Build and verify**

Run: `cd /home/leo/Projects/obi-wan-core && go build ./cmd/obi-wan-core/`
Expected: Build succeeds.

- [ ] **Step 4: Run all tests**

Run: `cd /home/leo/Projects/obi-wan-core && go test ./internal/core/ ./internal/tools/ ./internal/config/ ./internal/clients/watch/ ./internal/clients/telegram/ ./cmd/... -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
cd /home/leo/Projects/obi-wan-core
git add cmd/obi-wan-core/main.go config.yaml.example
git commit -m "feat(main): wire ToolRegistry into APIClient with Obsidian, Fastmail, and spawn tools"
```

---

### Task 8: Update System Prompts

**Files:**
- Modify: `~/.claude/channels/watch/system-prompt.md`
- Modify: `~/.claude/channels/telegram/system-prompt.md`

Replace the "Model Routing / Escalate to Opus" section with guidance about `spawn_claude_code`.

- [ ] **Step 1: Update watch system prompt**

In `~/.claude/channels/watch/system-prompt.md`, replace the "Model Routing" section (lines 96-108) with:

```markdown
## Heavy Tasks

For heavy tasks (research, showprep, council, wiki ingest, code review, deep analysis), use the `spawn_claude_code` tool. This fires off a full Claude Code process in the background with all skills, MCP servers, and tools. You get an instant confirmation; results will appear in Obsidian or wherever the skill writes them.

Use `spawn_claude_code` when:
- The user asks for research, deep analysis, or show prep
- The user invokes the council skill
- The user asks complex financial, legal, or technical questions
- The user asks you to write or review code
- The user asks for wiki ingest or large-scale content creation
- The message includes keywords: research, council, showprep, analyze, investigate, plan

Briefly tell the user you're kicking it off in the background.
```

- [ ] **Step 2: Update telegram system prompt**

Apply the same replacement in `~/.claude/channels/telegram/system-prompt.md` (lines 96-108).

- [ ] **Step 3: Commit** (these are outside the repo, so just note the change)

These files are in `~/.claude/channels/` which syncs via the claude config git repo. No commit needed in obi-wan-core.

---

### Task 9: Build, Deploy, and Test

- [ ] **Step 1: Build release binary**

```bash
cd /home/leo/Projects/obi-wan-core
go build -o obi-wan-core ./cmd/obi-wan-core/
```

- [ ] **Step 2: Update real config**

Add to `~/.config/obi-wan-core/config.yaml`:

```yaml
vault_root: ~/Obsidian/lgl
fastmail_token_env: FASTMAIL_API_TOKEN
fastmail_user: leo@fastmail.com
fastmail_password_env: FASTMAIL_PASSWORD
claude_binary: /home/leo/.local/bin/claude
```

Ensure `FASTMAIL_API_TOKEN` and `FASTMAIL_PASSWORD` are available in the systemd environment files.

- [ ] **Step 3: Deploy and restart**

```bash
systemctl --user stop obi-wan-core
cp ~/Projects/obi-wan-core/obi-wan-core ~/.local/bin/obi-wan-core
systemctl --user start obi-wan-core
journalctl --user -u obi-wan-core --since "5 sec ago"
```

Verify logs show: obsidian tools registered, fastmail tools registered, spawn_claude_code tool registered.

- [ ] **Step 4: Live tests**

From Watch or Telegram:
- "had a turkey sandwich for lunch" → should patch today's daily note meal table
- "exercise rowing 30 minutes 5000m" → should patch exercise section
- "appointment dentist Thursday at 2pm" → should create Fastmail calendar event
- "research Jeff Mahan San Jose mayor for showprep" → should spawn background claude process
- "how's it going?" → should respond normally (no tools used, fast path)

---

### Post-Implementation Checklist

- [ ] All tests pass (tools + core + config + clients)
- [ ] Binary builds and runs
- [ ] Config example updated with tool fields
- [ ] System prompts updated (watch + telegram)
- [ ] Live test: meal logging from Watch
- [ ] Live test: calendar event from Telegram
- [ ] Live test: spawn_claude_code for showprep
- [ ] Live test: normal conversation still fast
- [ ] Verify history.json doesn't include tool call internals
