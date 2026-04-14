# obi-wan-core

A unified Go backend that routes voice and text from multiple devices through the Anthropic Messages API. One dispatcher, multiple clients, conversation history, and native tool support.

## What it does

```
 Telegram DM ──┐
                │
 Apple Watch ───┤──▶ Dispatcher ──▶ Anthropic API ──▶ Reply
                │      ▲                  │
 Rabbit R1 ────┘      │             Tool Loop
                   history / memory     │
                   access control   ┌───┴───┐
                                    │ Tools │
                                    ├───────┤
                                    │Obsidian│ file ops
                                    │Fastmail│ calendar + contacts
                                    │Spawn  │ claude -p for heavy tasks
                                    └───────┘
```

You talk to it from your phone, your watch, or a Rabbit R1 — it all goes through the same conversation history with shared memory and per-channel system prompts. Replies route back to the device that sent the message.

## Architecture

**Core** (`internal/core/`) — The dispatcher accepts a `Turn` (channel + user + message), checks access control, loads conversation history and memory, calls the Anthropic Messages API directly, and returns a `Reply`. A concurrency semaphore prevents overloading. Conversation history is unified across all channels with token-budget pruning. Model escalation via `/opus` prefix.

**Tools** (`internal/tools/`) — Native tool runtime with a registry pattern. Tools are included in every API request; when Claude returns a `tool_use` response, the tool is executed locally and the result sent back in a loop. Seven tools:

- **obsidian_read_note** — Read a note from the Obsidian vault
- **obsidian_patch_note** — Insert content under a heading (meals, exercise, notes, tasks)
- **obsidian_write_note** — Create or overwrite a note
- **fastmail_create_event** — Create calendar events via CalDAV
- **fastmail_create_contact** — Create contacts via JMAP
- **fastmail_search_contacts** — Search contacts via JMAP
- **spawn_claude_code** — Fire-and-forget background `claude -p` for heavy tasks (research, showprep, code review) with full Claude Code skills and MCP access

**Clients** (`internal/clients/`) — Three input adapters, all feeding the same dispatcher:

- **Telegram** — Long-poll bot via [go-telegram/bot](https://github.com/go-telegram/bot). Handles message chunking for Telegram's 4096-char limit with rune-safe splitting.
- **Watch** — HTTP webhook server for Apple Watch dictation (via Shortcuts + Tailscale). Replies echo back to Telegram so you see them on your phone too.
- **R1** — WebSocket server implementing a subset of the [OpenClaw](https://github.com/nicholasgasior/openclaw) gateway protocol. The R1 connects thinking it's talking to an OpenClaw gateway; we handle QR pairing, ed25519 signature verification, and async message dispatch via the `chat.send` → `chat` event flow (see below).

**Fallback** (`internal/core/fallback.go`) — Multi-tier fallback chain. If the primary Anthropic API fails, falls back to alternate providers (e.g., z.ai GLM, local Ollama).

**Config** (`internal/config/`) — Single YAML file defines channels, access control, tool configuration, and secrets references (env var names, never values).

**Memory** (`internal/memory/`) — Per-channel memory files (`~/.claude/channels/<channel>/memory.md`) are loaded and combined with system prompts on every turn.

## Running it

```bash
# Build
go build -o obi-wan-core ./cmd/obi-wan-core

# Serve (long-running daemon — all enabled channels)
obi-wan-core serve --config ~/.config/obi-wan-core/config.yaml

# One-shot dispatch (useful for testing)
obi-wan-core dispatch --channel telegram --user 12345 --message "hello"
```

## Configuration

```yaml
api_key_env: ANTHROPIC_API_KEY
base_url: https://api.anthropic.com
state_dir: ~/.local/state/obi-wan-core
model: claude-sonnet-4-6
escalation_model: claude-opus-4-6
token_budget: 80000
concurrency: 2

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

# Tool support (optional)
vault_root: ~/Obsidian/lgl
fastmail_token_env: FASTMAIL_API_TOKEN
fastmail_user: your_email@fastmail.com
fastmail_password_env: FASTMAIL_PASSWORD
claude_binary: claude

channels:
  telegram:
    enabled: true
    bot_token_env: TELEGRAM_BOT_TOKEN
    allow_from: ["12345"]
    system_prompt_file: ~/.claude/channels/telegram/system-prompt.md

  watch:
    enabled: true
    webhook_port: 8199
    webhook_key_env: WEBHOOK_KEY
    watch_chat_id_env: WATCH_CHAT_ID

  r1:
    enabled: true
    webhook_port: 8200
    bootstrap_token_env: R1_BOOTSTRAP_TOKEN
    device_state_path: ~/.local/state/obi-wan-core/r1-device.json
```

Secrets are referenced by environment variable name — the binary never reads secret files directly. Works with sops, systemd `EnvironmentFile=`, or any secret injection method you prefer.

## How the R1 shim works

The Rabbit R1 (running [r1_escape](https://github.com/nicholasgasior/r1_escape) / OS 2) connects to an OpenClaw-compatible gateway over WebSocket. This project implements just enough of that protocol:

1. **QR pairing** — R1 scans a QR code containing the gateway URL. On first connect, it sends a bootstrap token; the server stores the device's ed25519 public key. Single-device pairing policy: one R1 per shim instance.
2. **Signature verification** — Subsequent reconnects are authenticated via signed payloads. Both v2 and v3 signature formats are accepted; the `operator` role is allowed alongside `node` because the real R1 firmware reports as operator.
3. **Async message routing** — Voice transcripts arrive as `chat.send` method calls. The shim returns an immediate `{runId, status:"started"}` ACK, dispatches through the core on a background goroutine (2-minute timeout), and pushes the reply back as a `chat` event with `state:"final"` when done. Errors come back as `chat` events with `state:"error"`.
4. **Device TTS fallback** — `talk.speak` and `talk.config` return a fallback-eligible `TALK_UNCONFIGURED` error, which tells the R1 firmware to use on-device Android TTS rather than server-side audio.
5. **Tick keepalive** — Server sends periodic ticks to keep the connection alive. HTTP pre-flight health checks are honored before the WebSocket upgrade.

The entire shim is ~2,000 lines including tests. No Docker, no OpenClaw installation required.

## Design decisions

- **Direct API, not `claude -p`** — Calls the Anthropic Messages API directly for fast, lightweight responses (~3-5K tokens vs ~47K with `claude -p`). Heavy tasks that need the full Claude Code environment (skills, MCP servers) are dispatched via the `spawn_claude_code` tool.
- **Unified conversation history** — One history file shared across all channels. One Leo, one Obi-Wan.
- **Native tool runtime** — Tools execute locally in the Go process (file I/O for Obsidian, HTTP for Fastmail). No MCP servers needed for core functionality.
- **Fail-open on memory** — If a memory file is missing or too large, dispatch continues without it.
- **Multi-tier fallback** — Primary API failure cascades through configured fallback providers.

## Requirements

- Go 1.22+
- Anthropic API key
- For tools: Obsidian vault, Fastmail account (optional)
- For spawn: [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed (optional)
- For R1: a Rabbit R1 running r1_escape with OpenClaw gateway support

## License

MIT
