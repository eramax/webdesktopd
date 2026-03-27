# `webdesktopd` – Architecture & Implementation Spec

## 1. Overview

`webdesktopd` is a self-contained daemon that exposes a persistent “web desktop” over HTTP:

- Each Unix user gets a **personal web desktop** accessible via a browser.
- The desktop includes a **real terminal**, **file manager**, **system status dock**, and **port-forward-like web app viewing** via a WebSocket tunnel.
- Authentication reuses **existing SSH credentials** (password or SSH key) by dialing the local `sshd`.
- The server binary runs as a dedicated low-privilege user with `cap_setuid,cap_setgid`, and **each PTY is run as the real Unix user**. `sudo` and root commands work inside that PTY exactly as in a native terminal.
- Transport is a **single WebSocket per browser session** with a small binary multiplexing protocol. HTTP is used only for `/auth`.

Primary goals:

- Minimal runtime dependencies (single Go binary + static SvelteKit build).
- Strong isolation: each user sees **only their own desktop**.
- Persistence: PTYs continue running when the browser disconnects; on reconnect, output is replayed from ring buffers.
- User experience close to a native terminal + lightweight desktop shell.

***

## 2. High-Level Architecture

### 2.1 Components

1. **Daemon (`webdesktopd`)**
   - Go binary, listens on `0.0.0.0:PORT` (configurable, default 8080).
   - Handles:
     - `POST /auth` → SSH-based auth → JWT.
     - `GET /` → serves SvelteKit SPA (embedded static assets).
     - `GET /ws?token=JWT` → upgrades to WebSocket, runs multiplexed protocol.

2. **Frontend (SvelteKit 5)**
   - SPA served from `/` and `/desktop`.
   - Key UI elements:
     - **Desktop shell** (wallpaper, draggable windows).
     - **Terminal window(s)** (vertical tab list + ghostty-web terminal).
     - **File manager** (tree + list, upload/download).
     - **System dock** (CPU, RAM, disk, net, processes, uptime, kernel, etc.).
     - **Port proxy tabs** for viewing apps on localhost:port.

3. **Local `sshd`**
   - Existing system SSH daemon on `localhost:22`.
   - Used for credential verification only. No port forwarding or command execution via SSH; we just validate username/password/key.

4. **OS / Processes**
   - For each terminal tab, `webdesktopd` spawns the user’s shell under that user’s UID/GID in a PTY.
   - `sudo`, `su`, `doas`, etc., work as usual via setuid.

### 2.2 Trust Model

- Threat: attacker compromises `webdesktopd`.
- Mitigation:
  - Run binary as dedicated user (e.g. `webdesktopd`) with capabilities `cap_setuid,cap_setgid` only.
  - All privileged operations are limited to dropping into a user’s UID/GID before spawning processes.
  - No root shell inside `webdesktopd`; only child processes running as users.
  - TLS is terminated at reverse proxy / load balancer (e.g. Caddy/NGINX); `webdesktopd` expects HTTP/WS behind TLS.

***

## 3. Technologies and Libraries

### 3.1 Backend (Go)

- Language: **Go ≥ 1.26**
- Core libs:
  - `net/http` – HTTP, WS upgrade (via `github.com/gorilla/websocket` or `nhooyr.io/websocket`).
  - `golang.org/x/crypto/ssh` – SSH client for credential validation.
  - `github.com/creack/pty` – PTY creation and resizing.
  - `github.com/golang-jwt/jwt/v5` – JWT signing and verification.
  - `encoding/binary` – binary frame protocol.
- OS APIs:
  - `os/exec`, `syscall.SysProcAttr`, `syscall.Credential` – spawn shell with setuid/setgid.
  - `/proc` parsing on Linux for stats (cross-platform is not a requirement initially).

### 3.2 Frontend (SvelteKit)

- Framework: **SvelteKit 5** with Vite.
- Terminal:
  - **Primary target**: `ghostty-web` WebAssembly terminal with xterm.js-compatible API (requires bundling WASM).
- State:
  - Svelte stores for session state / window layout.
  - Persistent UI config per user saved server-side (JSON) + optional client-side caching.
- Networking:
  - `fetch` for `POST /auth`.
  - Single `WebSocket` client for all runtime interaction.
  - Service Worker for port-proxy virtual host redirect (optional phase).

***

## 4. Runtime Model

### 4.1 Process & Capability Layout

- `webdesktopd` runs as e.g. `uid=995(webdesktopd)` with:
  ```bash
  setcap cap_setuid,cap_setgid+ep /usr/local/bin/webdesktopd
  ```
- On successful auth:
  - The daemon **does NOT** keep an SSH channel. It just verifies credentials and returns a JWT.
  - Later, when a PTY is needed, it:
    - Looks up `uid` and `gid` for username via `/etc/passwd` or `os/user`.
    - Spawns shell via `exec.Command` with `SysProcAttr.Credential` set to that UID/GID.
- All terminal commands, including `sudo`, run under the correct Unix permissions.

### 4.2 Session & PTY Lifecycle

**Per-user session**:

- Key: `userSessionID` derived from username (and optional device ID later).
- Contains:
  - One or more `PTYSession`s (terminal tabs).
  - One or more `PortProxySession`s (TCP tunnels).
  - Desktop UI state (windows, wallpaper, tab labels).

**PTYSession**:

- Holds:
  - `cmd` (running shell process).
  - `pty` fd.
  - `ring buffer` of bytes (fixed capacity).
  - Channel id (uint16) for the WS mux layer.
- Goroutines:
  - Reader: copy from `pty` to ring buffer and push frames to WS connection (through mux hub) if available.
  - Optional writer: data from WS forwarded to PTY.

### 4.3 Reconnect Semantics

- When the browser disconnects:
  - `WebSocket` is closed.
  - PTY processes **keep running**; their goroutine continues reading and writing only to the ring buffer.
- On reconnect:
  - Client sends `GET /ws?token=JWT`.
  - Server reattaches this WS to the existing `UserSession` (identified from JWT).
  - Server sends:
    1. State sync frame type `0x0C` describing:
       - Open PTY channels.
       - Port proxy channels.
       - Desktop state (or this can be separate `0x12`).
    2. For each PTY:
       - Replay ring buffer tail as `0x01` frames in order.
    3. Switch to live streaming (PTY goroutines now write to WS).

No PTY is killed or reset on reconnect; from user perspective, the terminal looks like it was just resumed.

***

## 5. Protocol

### 5.1 HTTP

- `POST /auth`
  - Body: JSON: `{ "username": "...", "password": "...", "privateKeyPem": "..." }`
    - Only one of `password` or `privateKeyPem` is required (configurable).
  - Flow:
    1. Use `golang.org/x/crypto/ssh` to dial `localhost:22`.
    2. Attempt `ssh.ClientConfig` auth with provided creds.
    3. On success: close SSH connection, issue JWT: `sub=username`, `exp=...`.
  - Responses:
    - 200 `{ "token": "JWT_STRING" }`
    - 401 on failed auth.

- Static assets:
  - `GET /` and all `/assets/*` – served via embedded SvelteKit `dist` directory.

- No other REST endpoints (v1). All runtime control after auth is via WS.

### 5.2 WebSocket (Single Connection)

URL: `GET /ws?token=JWT`

#### 5.2.1 Frame Envelope

Binary frames only.

```
+--------+--------+----------+---------------------+
| 1 byte | 2 bytes| 4 bytes  | N bytes             |
+--------+--------+----------+---------------------+
| type   | chanID | length   | payload (raw bytes) |
+--------+--------+----------+---------------------+
```

- `type` – frame type (see table below).
- `chanID` – logical channel ID (0-65535).
- `length` – payload length (uint32, big endian).
- `payload` – raw bytes.

#### 5.2.2 Frame Types

| Type (hex) | Direction | Description |
|---|---|---|
| `0x01` | both | Raw data (PTY stdin/out, TCP proxy data) |
| `0x02` | C→S  | PTY resize – payload: `{ "cols": N, "rows": M, "channel": chanID }` or compact binary |
| `0x03` | S→C  | Stats tick – payload: JSON metrics object (CPU, RAM, etc.) |
| `0x04` | C→S  | File list request – payload: path |
| `0x05` | S→C  | File list response – payload: JSON `[]FileInfo` |
| `0x06` | C→S  | File upload chunk – payload: `[uploadID|path|offset|bytes...]` (defined precisely in impl) |
| `0x07` | S→C  | File upload/download progress – payload: JSON `{ "id": "...", "bytesSent": N, "total": M }` |
| `0x08` | C→S  | File download request – payload: JSON `{ "id": "...", "path": "..." }` |
| `0x09` | S→C  | File download chunk – payload: `[downloadID|offset|bytes...]` |
| `0x0A` | C→S  | Open new PTY – payload: JSON `{ "channel": chanID, "shell": "/bin/bash", "cwd": "$HOME" }` |
| `0x0B` | C→S  | Close PTY – payload: `{ "channel": chanID }` |
| `0x0C` | S→C  | Session sync – payload: JSON session state (channels + UI state) |
| `0x0D` | C→S  | Ping – payload empty or small nonce |
| `0x0E` | S→C  | Pong – payload echo of ping |
| `0x0F` | C→S  | Open port proxy – payload: JSON `{ "channel": chanID, "target": "127.0.0.1:3000" }` |
| `0x10` | C→S  | Close port proxy – payload: `{ "channel": chanID }` |
| `0x11` | C→S  | File operation – payload: JSON (rename/delete/chmod) |
| `0x12` | S→C  | Desktop state push – payload: JSON (windows, wallpaper, tab names) |
| `0x13` | C→S  | Desktop state save – payload: JSON (same shape as above) |

Notes:

- Stats are broadcast on channel `0`. Other channel IDs are per PTY or per proxy.
- File ops are logically multiplexed as well; they can share a dedicated channel or be keyed by `operationID` in the payload.

***

## 6. Feature Design

### 6.1 Authentication & Authorization

**User Flow**:

1. Login page asks for:
   - Username.
   - Password or private key (with optional passphrase).
2. Frontend sends `POST /auth`.
3. Backend performs an SSH client connection to `localhost:22` using these credentials.
4. On success:
   - Return JWT with `sub=username`.
   - Frontend stores JWT (memory or localStorage).
5. Frontend navigates to `/desktop` and opens WS with that token.

**Authorization**:

- Every WS connection is accepted only if JWT is valid and unexpired.
- `username` from JWT determines which `UserSession` to attach to.
- There is no multi-user admin UI in v1; each user owns only their own sessions.

### 6.2 Terminal Tabs

- Frontend:
  - Vertical tab list on the left.
  - Each tab corresponds to a `chanID`.
  - Each tab contains one ghostty-web instance.
- Backend:
  - `0x0A` creates a new PTY:
    - Resolve user home directory.
    - Spawn shell as that user, with PTY.
  - PTY output:
    - Goes into ring buffer (cap e.g. 1MB or 10k lines).
    - If WS is connected: send as `0x01` frames for that `chanID`.
  - PTY input:
    - `0x01` frames from client go directly into PTY.

### 6.3 Reconnection

- On reconnect:
  - Server identifies `UserSession` by JWT.
  - Sends `0x0C` (session sync) payload:
    - List of `PTYSession`s: `[{ channel, cwd, title }, ...]`.
    - List of proxies: `[{ channel, target }, ...]`.
    - Desktop state.
  - For each PTY:
    - Replay ring buffer via `0x01` frames.
  - Resume live streaming.

### 6.4 File Manager

- Default root: `$HOME`.
- User can navigate anywhere they have permission (e.g. `/etc`).
- Operations:
  - List directory: `0x04` request with path → `0x05` response with JSON like:
    ```json
    [
      { "name": "file.txt", "size": 1234, "isDir": false, "mode": "0644", "modTime": "2025-03-01T12:00:00Z" },
      { "name": "folder", "size": 4096, "isDir": true, "mode": "0755", "modTime": "..." }
    ]
    ```
  - Upload:
    - Frontend splits file into 64KB chunks.
    - Sends them as `0x06` frames with upload ID/path/offset.
    - Backend writes to a temp file, then moves to final location.
    - Backend sends `0x07` frames with progress.
  - Download:
    - Client sends `0x08` with path.
    - Backend reads file and sends `0x09` frames (64KB).
    - Progress `0x07` frames accompany the download.

### 6.5 System Stats Dock

- Metrics fetched server-side from `/proc`:
  - CPU usage % & load averages.
  - RAM used/free.
  - Disk usage + read/write rates.
  - Network RX/TX rates.
  - Process count, thread count.
  - Uptime, OS name, kernel version.
  - GPU stats (if available) via vendor tools or `/sys`.
- Logic:
  - A single `StatsCollector` goroutine runs when at least one `UserSession` is attached.
  - Emits aggregated metrics every N ms (e.g. 1000ms).
  - Server sends JSON metrics as `0x03` frames on channel `0`.
  - Frontend dock subscribes to these frames and updates UI.

### 6.6 Desktop UI State

- State includes:
  - Wallpaper selection.
  - Window list with positions/sizes.
  - Terminal tab labels.
- Persistence:
  - Frontend sends `0x13` payload on meaningful changes (debounced).
  - Backend writes `~/.webdesktopd/state.json` for that user.
  - On login/reconnect, backend loads this and includes it in `0x0C` (or `0x12`).

### 6.7 Port Proxy / “Localhost in Tab”

- User opens “Port Proxy” tab and specifies `localhost:3000`.
- Frontend picks a new `chanID`, sends `0x0F` with target `"127.0.0.1:3000"`.
- Backend:
  - Opens TCP connection to that target as the user (using `DialContext`).
  - Starts copying:
    - TCP→WS: `0x01` frames for that `chanID`.
    - WS→TCP: `0x01` frames back into the socket.
- Frontend:
  - Implemented with a Service Worker that intercepts requests to a custom domain such as `https://port-3000.local.webdesktopd/` and tunnels HTTP via the WS channel.
  - For phase 1, it may be enough to send raw HTTP from the browser UI (e.g. inside a `<iframe>` that uses JS to tunnel). Details can be refined later.

***

## 7. Constraints & Non-Goals

- Platform: **Linux** only (uses `/proc`, `/sys`, POSIX PTYs).
- No built-in TLS: handled by reverse proxy.
- No multi-tenant cross-user visibility in v1.
- No full desktop (X11/Wayland) forwarding; terminal + basic windows only.
- Browser: modern Chromium/Firefox; no IE or old Edge.

***

## 8. Implementation Guide: Spec → Tests → Code

This section is meant to drive the development process. For each feature, we define:

- Spec (behavior contract).
- Required tests.
- Implementation notes.

### 8.1 Auth & JWT

**Spec**

- POST `/auth` with valid SSH creds returns 200 + JWT.
- Invalid creds → 401.
- JWT includes `sub=username`, `exp=...`, signed with server secret.
- WS `/ws?token=JWT` rejects invalid/expired tokens with 401/close.

**Tests**

1. **Unit: SSH Auth Success**
   - Given a test `sshd` container and known user/pass:
     - `POST /auth` with correct creds → status 200, body has `token`.
2. **Unit: SSH Auth Failure**
   - Invalid password → 401.
3. **Unit: JWT Expiry**
   - Create token with very short lifetime → WS connection fails after expiry.
4. **Unit: JWT Claims**
   - Token `sub` matches provided username.

**Implementation Notes**

- Use a configurable SSH address (default `localhost:22`).
- Use `context.WithTimeout` for SSH dial.
- Secret key for JWT from env or config.
- Use `github.com/golang-jwt/jwt/v5`.

***

### 8.2 WebSocket Hub & Multiplexing

**Spec**

- `/ws` upgrades to a single connection per browser session.
- Binary frames follow envelope layout above.
- Frames routed by `chanID` to appropriate session handler:
  - `0` → broadcast / stats.
  - `>=1` → PTY or port proxy.
- Heartbeat pings/pongs keep the connection alive; idle timeouts close it gracefully.

**Tests**

1. **Unit: Frame Encode/Decode**
   - For various payload sizes and types, encode then decode equals original.
2. **Unit: Dispatch by Channel**
   - Create two fake channels, send frames for chan 1 and chan 2, assert the correct handler receives them.
3. **Integration: WS Lifetime**
   - Connect WS, send ping frames, ensure connection stays open.
4. **Integration: Unauthorized WS**
   - Connect with expired/invalid JWT → immediate close.

**Implementation Notes**

- Implement a `Hub` type managing:
  - `conn *websocket.Conn`.
  - `channels map[uint16]ChannelHandler`.
- Channel handler interface:
  ```go
  type ChannelHandler interface {
      HandleFrame(frame Frame)
      Close()
  }
  ```
- Concurrency:
  - Single writer goroutine per WS, to avoid concurrent writes.
  - Reader goroutine decodes frames and dispatches.

***

### 8.3 PTY Session & Reconnection

**Spec**

- `0x0A` (Open PTY) creates a PTY for the authenticated user.
- PTY stdout is streamed as `0x01` frames to the client.
- Arbitration:
  - When no client is connected, output only goes into ring buffer.
- On reconnect:
  - `0x0C` is sent with session info.
  - Ring buffer content is replayed via `0x01` frames before live streaming.

**Tests**

1. **Unit: PTY Spawn as User**
   - Given `username`, ensure `exec.Cmd` runs with correct UID/GID.
2. **Unit: Ring Buffer**
   - Write > capacity bytes, ensure only last N bytes are preserved.
   - Verify sequential read of buffer yields expected bytes.
3. **Integration: Live Session**
   - Connect, open PTY, send command `echo hello`, assert client receives `hello`.
4. **Integration: Reconnect**
   - Start PTY, send `sleep 1; echo hi`, disconnect client.
   - After 1s, reconnect with same JWT.
   - Assert `hi` appears via replay.

**Implementation Notes**

- Use `github.com/creack/pty.StartWithAttrs`.
- Configure `SysProcAttr` with `Credential` and `Setsid`.
- Reader goroutine must:
  - `io.Copy` from PTY into:
    - ring buffer,
    - optional channel for WS writer.
- Writer side: frames from WS → `pty.Write`.

***

### 8.4 File Manager

**Spec**

- `0x04` with path lists directory.
- `0x06` uploads file in 64KB chunks, `0x07` progress.
- `0x08`+`0x09` download file in chunks, `0x07` progress.
- Files are owned by the Unix user and respect file system permissions.

**Tests**

1. **Unit: List Directory**
   - On a test directory, verify JSON structure matches actual contents.
2. **Integration: Upload**
   - Send file in chunks, ensure final file content matches original.
   - Progress frames reach 100%.
3. **Integration: Download**
   - Request download, ensure concatenated chunks equal original file.
4. **Security: Permission Errors**
   - Attempt to upload into a directory without write permission → error response.

**Implementation Notes**

- Limit max file size (config) to prevent resource exhaustion.
- Store partial uploads in a temp dir under user’s home, then atomically rename.
- Use context timeouts for file operations to avoid hanging.

***

### 8.5 Stats Dock

**Spec**

- When at least one `UserSession` exists, `StatsCollector` runs.
- Every N ms emits metrics as JSON via `0x03` on channel `0`.
- Dock shows CPU, RAM, disk, net, processes, uptime, OS, kernel.

**Tests**

1. **Unit: Metrics Calculation**
   - On a controlled `/proc` fixture, verify CPU / RAM calculations.
2. **Unit: Ref-count**
   - Adding the first session starts collector; removing last stops it.
3. **Integration: Live Updates**
   - Client receives updates every N ms while connected.

**Implementation Notes**

- Use `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`, `/proc/net/dev`, `/proc/diskstats`.
- Store previous snapshots for delta-based MB/s calculations.

***

### 8.6 Desktop State Persistence

**Spec**

- Frontend sends `0x13` with UI state on changes.
- Server writes JSON to `~/.webdesktopd/state.json`.
- On login/reconnect, server reads this file and includes it in `0x0C` (or sends a `0x12` after sync).
- State is namespaced per username.

**Tests**

1. **Unit: State Read/Write**
   - Write state, then read → equality.
2. **Integration: Persisted Layout**
   - Arrange windows, reload page, verify layout restored.

**Implementation Notes**

- Use a small struct like:
  ```go
  type DesktopState struct {
      Wallpaper string            `json:"wallpaper"`
      Windows   []WindowState     `json:"windows"`
      Tabs      []TerminalTabMeta `json:"tabs"`
  }
  ```
- Use `os.MkdirAll("~/.webdesktopd", 0700)`.

***

### 8.7 Port Proxy

**Spec**

- `0x0F` opens a TCP connection to `target` (e.g. `127.0.0.1:3000`) under the user’s UID.
- `0x01` frames on that `chanID` carry raw TCP bytes.
- `0x10` closes the proxy and TCP connection.
- Frontend uses a Service Worker or higher-level tunneling logic to present this as a real web app view.

**Tests**

1. **Integration: Simple HTTP**
   - Start a local HTTP server on 3000, proxy it with `0x0F`.
   - Make a GET request tunneled via WS, ensure response is correct.
2. **Error: Target Unreachable**
   - If TCP dial fails, send error frame and do not create channel.

**Implementation Notes**

- Use `net.DialTCP` after privilege drop to user’s UID, if needed.
- Integrate with the same mux infrastructure as PTYs.

***

## 9. Phasing

| # | Phase | Status | Notes |
|---|---|---|---|
| 1 | **Core backend skeleton** — HTTP server, `/auth`, JWT, WS hub | ✅ Done | |
| 2 | **PTY sessions** — spawn shell, stream to/from WS, reconnect | ✅ Done | Ring buffer replay on reconnect |
| 3 | **Multi-tab support** — vertical tabs, multiple PTYs | ✅ Done | Per-channel routing |
| 4 | **Stats collector + dock** — `/proc` metrics, `0x03` frames, dock UI | ✅ Done | CPU, RAM, disk, net, load, uptime, kernel |
| 5 | **File manager** — list, upload/download, mkdir, touch, copy, cut/paste, delete, rename | ✅ Done | Full UI in `FileManager.svelte`; `homeDir` in session sync; e2e tests pass |
| 6 | **Desktop persistence** — `state.json`, `0x12`/`0x13` frames | ⬜ Pending | |
| 7 | **Port proxy** — TCP tunnel, `0x0F`/`0x10` frames, frontend view | ⬜ Pending | |
| 8 | **Polish & hardening** — error handling, logging, metrics, config | ⬜ Pending | |

***
