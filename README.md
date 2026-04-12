# obi-wan-core

A unified Go backend that routes voice and text from multiple devices through [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude -p`). One dispatcher, multiple clients, persistent sessions and memory.

## What it does

```
 Telegram DM ──┐
                │
 Apple Watch ───┤──▶ Dispatcher ──▶ claude -p ──▶ Reply
                │      ▲
 Rabbit R1 ────┘      │
                   sessions / memory / access control
```

You talk to it from your phone, your watch, or a Rabbit R1 — it all goes through the same Claude session with shared memory and per-channel system prompts. Replies route back to the device that sent the message.

## Architecture

**Core** (`internal/core/`) — The dispatcher accepts a `Turn` (channel + user + message), checks access control, loads the session and memory file, shells out to `claude -p`, and returns a `Reply`. A concurrency semaphore prevents overloading. Sessions auto-rotate on error.

**Clients** (`internal/clients/`) — Three input adapters, all feeding the same dispatcher:

- **Telegram** — Long-poll bot via [go-telegram/bot](https://github.com/go-telegram/bot). Handles message chunking for Telegram's 4096-char limit with rune-safe splitting.
- **Watch** — HTTP webhook server for Apple Watch dictation (via Shortcuts + Tailscale). Replies echo back to Telegram so you see them on your phone too.
- **R1** — WebSocket server implementing a subset of the [OpenClaw](https://github.com/nicholasgasior/openclaw) gateway protocol. The R1 connects thinking it's talking to an OpenClaw gateway; we handle QR pairing, ed25519 signature verification, and async message dispatch. Round-trip latency is ~1-2 seconds.

**Config** (`internal/config/`) — Single YAML file defines channels, access control, and secrets references (env var names, never values).

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
claude_binary: /home/you/.local/bin/claude
state_dir: ~/.local/state/obi-wan-core
concurrency: 2

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
```

Secrets are referenced by environment variable name — the binary never reads secret files directly. Works with sops, systemd `EnvironmentFile=`, or any secret injection method you prefer.

## How the R1 shim works

The Rabbit R1 (running [r1_escape](https://github.com/nicholasgasior/r1_escape) / OS 2) connects to an OpenClaw-compatible gateway over WebSocket. This project implements just enough of that protocol:

1. **QR pairing** — R1 scans a QR code containing the gateway URL. On first connect, it sends a bootstrap token; the server stores the device's ed25519 public key.
2. **Signature verification** — Subsequent connections are authenticated via signed payloads (v2 format).
3. **Message routing** — Voice transcripts arrive as `sessions.send` method calls, get dispatched through the core as a `Turn`, and replies push back as `chat` events.
4. **Tick keepalive** — Server sends periodic ticks to keep the connection alive.

The entire shim is ~2,000 lines including tests. No Docker, no OpenClaw installation required.

## Design decisions

- **Wraps `claude -p`, not the API** — This is Claude Code's full agent loop (tool use, file access, permissions) via subprocess, not a raw API client. `--permission-mode auto` handles tool approvals.
- **Channels are isolated** — Each channel gets its own session, memory file, and system prompt. A Telegram conversation doesn't bleed into R1.
- **Fail-open on memory** — If a memory file is missing or too large, dispatch continues without it. The conversation still works; you just lose context.
- **Session rotation** — If `claude -p` returns a session error, the dispatcher rotates to a fresh session and retries once automatically.

## Requirements

- Go 1.22+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- For R1: a Rabbit R1 running r1_escape with OpenClaw gateway support

## License

MIT
