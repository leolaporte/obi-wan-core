# obi-wan-core

Unified Obi-Wan backend service. Routes turns from multiple client interfaces (Telegram, Apple Watch webhook, Rabbit R1 WebSocket shim) through a shared dispatcher that wraps `claude -p` subprocess invocations.

Built in phases:
- **Plan 1 (this one):** Foundation — config, dispatcher, session/access/memory, systemd
- **Plan 2:** Telegram + Watch migration from `telegram-daemon`
- **Plan 3:** Rabbit R1 WebSocket shim implementing OpenClaw-compatible wire protocol

See `~/Obsidian/lgl/AI/Plans/` for current plan docs.

## Development

```bash
go build ./...
go test ./...
go run ./cmd/obi-wan-core
```
