# Tool Support for Direct API Path

**Date:** 2026-04-13
**Status:** Approved

## Problem

The direct API migration eliminated ~47K tokens of overhead per message but also removed all tool access — Obsidian file writes, Fastmail calendar/contacts, and Claude Code skills. Key use cases (meal logging, exercise tracking, notes, appointments, shopping lists, showprep) no longer work from Watch/Telegram/R1.

## Solution

Add a lightweight native tool runtime to obi-wan-core. Tool schemas are included in every API request (~1.5K tokens). The APIClient gains a tool loop: when Claude returns a `tool_use` response, obi-wan-core executes the tool locally and sends the result back. For heavy tasks (research, showprep), a `spawn_claude_code` tool fires off `claude -p` in the background with the full Claude Code environment.

## Architecture

```
Message arrives
    |
Dispatcher (unchanged)
    |
FallbackRunner (unchanged)
    |
APIClient.Send() — now with tool loop
    |
    +-- Response: text? --> return (same as now)
    |
    +-- Response: tool_use? --> ToolRegistry.Execute()
            |                     --> send result back
            |                     --> loop until text
            |
    ToolRegistry
    +-- obsidian_read_note
    +-- obsidian_patch_note
    +-- obsidian_write_note
    +-- fastmail_create_event
    +-- fastmail_create_contact
    +-- fastmail_search_contacts
    +-- spawn_claude_code
```

### Two-Speed Model

**Fast path (95% of messages):** Direct API + native tools. Conversation, meal logging, exercise, notes, shopping, calendar, contacts. Responds in 2-3 seconds even with tool use.

**Heavy path (on demand):** `spawn_claude_code` fires `claude -p` in the background with a specified skill. Instant acknowledgment to the user, heavy work runs asynchronously. Results land in Obsidian or wherever the skill writes them.

## Tool Loop

The APIClient's `Send` method changes from a single POST to a loop:

1. POST `/v1/messages` with `tools: [...]` in the body
2. Check `stop_reason` in response:
   - `end_turn` → extract text, return (normal path)
   - `tool_use` → extract tool name + input from content blocks
3. Execute tool via `ToolRegistry.Execute(name, input)` → get result string
4. Append assistant response + tool result to messages array
5. POST again, repeat from step 2

### Safety Bounds

- Max 10 tool loop iterations per dispatch (prevents runaway)
- 10-second timeout per individual tool execution
- Tool errors are sent back to Claude as the tool result text — Claude tells the user what went wrong

### Token Impact

- Tool schemas add ~1.5K tokens to every request (always included)
- Messages that don't use tools pay this overhead but skip the loop
- A food log message typically does 1-2 tool calls (read + patch), adding ~1-2s latency
- Total per-message cost: ~3-5K tokens vs. the old ~47K

## Tool Definitions

### obsidian_read_note

Read a note from the Obsidian vault.

- **Input:** `{"path": "Daily Notes/2026/04/2026-04-13.md"}`
- **Output:** File contents as string, or error if not found
- **Implementation:** `os.ReadFile(filepath.Join(vaultRoot, path))`
- **Vault root:** `~/Obsidian/lgl/` (from config)

### obsidian_patch_note

Insert text under a heading in an existing note. This is the workhorse — used for meals, exercise, voice notes, tasks, shopping.

- **Input:** `{"path": "Daily Notes/2026/04/2026-04-13.md", "heading": "#### Meals", "content": "| Lunch | Turkey sandwich | 350 | ... |", "position": "append"}` 
- **Position:** `append` (after existing content under heading) or `replace` (replace content under heading until next heading)
- **Output:** "OK" or error
- **Implementation:** Read file, find heading line, find next heading (or EOF), insert content, write file

### obsidian_write_note

Create or overwrite a note.

- **Input:** `{"path": "...", "content": "---\ntags: ...\n---\n..."}`
- **Output:** "OK" or error
- **Implementation:** `os.MkdirAll` for parent dirs + `os.WriteFile`

### fastmail_create_event

Create a calendar event via Fastmail JMAP.

- **Input:** `{"title": "Dentist", "start": "2026-04-15T14:00:00", "duration": "PT1H", "location": "...", "calendar": "Personal"}`
- **Output:** Confirmation string with event details
- **Implementation:** JMAP `CalendarEvent/set` call to Fastmail API
- **Auth:** Fastmail API token from env var (config: `fastmail_token_env`)

### fastmail_create_contact

Create or update a contact via Fastmail JMAP.

- **Input:** `{"name": "Jeff Mahan", "email": "...", "phone": "...", "company": "...", "notes": "..."}`
- **Output:** Confirmation string
- **Implementation:** JMAP `Contact/set` call
- **Note:** If updating, caller (Claude) should search first and provide the contact ID

### fastmail_search_contacts

Search contacts by name.

- **Input:** `{"query": "Jeff Mahan"}`
- **Output:** JSON array of matching contacts with IDs, names, emails
- **Implementation:** JMAP `Contact/query` + `Contact/get`

### spawn_claude_code

Fire off a `claude -p` process in the background for heavy tasks.

- **Input:** `{"task": "Research Jeff Mahan, San Jose mayor, for showprep", "skill": "showprep"}`
- **Output:** Immediate confirmation: "Task spawned"
- **Implementation:** `exec.Command("claude", "-p", "--model", "opus", task)` started with `cmd.Start()` (not `cmd.Wait()`). Process inherits the full Claude Code environment — MCP servers, skills, plugins, tools. The `skill` field is optional; if provided, the task is prefixed with `/<skill> `.
- **Fire-and-forget:** The process runs independently. Results land wherever the skill writes them (Obsidian, email, etc.). obi-wan-core does not track completion.
- **Binary path:** From config (new field: `claude_binary`, optional, defaults to `claude` in PATH)

## File Structure

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/tools/registry.go` | ToolRegistry: schema list + execute dispatch |
| Create | `internal/tools/obsidian.go` | Obsidian read/patch/write handlers |
| Create | `internal/tools/fastmail.go` | Fastmail JMAP handlers |
| Create | `internal/tools/claude.go` | spawn_claude_code handler |
| Create | `internal/tools/registry_test.go` | Registry tests |
| Create | `internal/tools/obsidian_test.go` | Obsidian tool tests (temp dir fixtures) |
| Create | `internal/tools/fastmail_test.go` | Fastmail tests (httptest mock JMAP) |
| Create | `internal/tools/claude_test.go` | Spawn tool tests |
| Modify | `internal/core/api.go` | Add tool loop to Send(), tool schema in request |
| Modify | `internal/core/api_test.go` | Test tool loop with mock tool_use responses |
| Modify | `internal/config/config.go` | Add `VaultRoot`, `FastmailTokenEnv`, `ClaudeBinary` fields |
| Modify | `cmd/obi-wan-core/main.go` | Create ToolRegistry, pass to APIClient |

## Config Changes

```yaml
# New fields
vault_root: ~/Obsidian/lgl
fastmail_token_env: FASTMAIL_API_TOKEN
claude_binary: claude   # optional, for spawn_claude_code
```

## What Changes

### Modified
- `APIClient.Send()` — tool loop, tool schemas in request body
- `apiRequest` struct — adds `Tools` field
- `apiResponse` struct — handles `tool_use` content blocks
- Config — new fields

### New
- `internal/tools/` package — registry + all tool handlers

### Unchanged
- Dispatcher, FallbackRunner, History, session-free architecture
- All client code (Telegram, Watch, R1)
- Conversation history (tool calls are between the user message and final response — only user + assistant text gets saved to history)

## System Prompt Changes

Remove the "Model Routing / Escalate to Opus" sections from watch and telegram system prompts (references Agent spawning, a Claude Code concept). Replace with guidance that Claude should use `spawn_claude_code` for heavy tasks like research, showprep, council, wiki ingest. The keyword commands stay as-is — Claude naturally maps them to the available tools.

## Extensibility

Adding a new tool requires:
1. Define the schema (name, description, input JSON schema) 
2. Write a handler function `func(ctx, input) (string, error)`
3. Register it in the ToolRegistry

No changes to the API client, dispatcher, or config needed.
