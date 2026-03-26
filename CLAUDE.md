# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`webdesktopd` is a self-contained daemon that exposes a persistent web desktop over HTTP/WebSocket. Each Unix user gets a personal desktop accessible via a browser, featuring a real terminal (with ghostty-web WASM), file manager, system stats dock, and port proxy. This project is currently in the specification/initial implementation phase — `architecture.md` is the authoritative design document.

## Stack

- **Backend**: Go ≥ 1.26 (single binary)
- **Frontend**: SvelteKit 5 + Vite (compiled to static assets embedded in the Go binary)
- **Terminal**: ghostty-web (WebAssembly, xterm.js-compatible API)
- **Platform**: Linux only (requires `/proc`, `/sys`, POSIX PTYs)

## Build Commands

Once implemented, the expected build flow is:

```bash
# Frontend
cd frontend && npm install && npm run build

# Backend (embeds frontend dist)
go build ./...

# Run
./webdesktopd --port 8080
```

## Testing

```bash
go test ./...
go test ./... -run TestName   # single test
```

Integration tests require a local `sshd` on `localhost:22` with a known test user.

## Key Architecture

### Transport & Protocol

- Single WebSocket per browser session (`GET /ws?token=JWT`) multiplexes all communication using a 7-byte binary frame envelope: `[type:1][chanID:2][length:4][payload:N]`.
- Channel `0` is reserved for broadcast stats (`0x03` frames). Channels ≥1 are per PTY or port proxy.
- `POST /auth` is the only REST endpoint — it dials `localhost:22` via SSH to validate credentials, then returns a JWT. No SSH connection is kept after validation.
- See `architecture.md` Section 5 for the full frame type table (types `0x01`–`0x13`).

### Process & Privilege Model

- Daemon runs as a dedicated low-privilege user with `cap_setuid,cap_setgid` only.
- PTY shells are spawned using `syscall.Credential` to drop to the authenticated user's UID/GID — all child processes run as that user.
- TLS is terminated upstream (Caddy/NGINX); daemon expects plain HTTP/WS.

### Session & Reconnection

- `UserSession` (keyed by username from JWT) persists across WebSocket disconnects; PTY processes keep running.
- Each `PTYSession` maintains a ring buffer (~1MB). On reconnect, the server replays the ring buffer tail as `0x01` frames, then switches to live streaming.
- Desktop UI state is saved server-side at `~/.webdesktopd/state.json` and included in the `0x0C` session sync frame on reconnect.

### Core Dependencies

| Package | Purpose |
|---|---|
| `golang.org/x/crypto/ssh` | SSH dial for credential validation |
| `github.com/creack/pty` | PTY creation and resizing |
| `github.com/golang-jwt/jwt/v5` | JWT signing/verification |
| `github.com/gorilla/websocket` or `nhooyr.io/websocket` | WebSocket handling |

## Development Phasing

Follow the order in `architecture.md` Section 9:
1. Core backend: HTTP server, `/auth`, JWT, WS hub with frame dispatch
2. PTY sessions (single tab, streaming)
3. Ring buffer + reconnect replay
4. Multi-tab support
5. Stats collector (reads `/proc`) + dock UI
6. File manager (list/upload/download via `0x04`–`0x09` frames)
7. Desktop state persistence (`0x12`/`0x13`)
8. Port proxy TCP tunnel (`0x0F`/`0x10`)
9. Polish & hardening
