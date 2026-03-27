# webdesktopd – Implementation Roadmap

## Status Legend
- `[ ]` Not started
- `[~]` In progress
- `[x]` Complete
- `[!]` Blocked / needs attention

---

## Phase 1 – Core Backend + Basic Terminal ✅ COMPLETE

### Backend
- [x] Project setup: `go.mod`, directory structure, `Makefile`
- [x] `internal/ringbuf` – fixed-capacity circular buffer (write overwrites oldest)
- [x] `internal/auth` – SSH credential validation → JWT issuance
- [x] `internal/hub` – binary frame protocol + single-WS multiplexer
- [x] `internal/pty` – PTY session (spawn shell as user, ring buffer, hub attach/detach)
- [x] `internal/server` – HTTP server: `POST /auth`, `GET /ws`, static assets
- [x] `main.go` – CLI flags, config, server startup, embedded frontend via `//go:embed`

### Frontend
- [x] SvelteKit 5 + Bun + TailwindCSS v4 project setup
- [x] Login page (`/`) – username/password form → `POST /auth` → store JWT
- [x] Desktop shell (`/desktop`) – minimal window chrome, WS connection
- [x] Terminal component – xterm.js bound to PTY channel
- [x] WS protocol client – binary frame encode/decode in TypeScript

### Tests
- [x] Unit: ring buffer (write/read, overflow/overwrite, concurrent) – 9 tests PASS
- [x] Unit: frame encode/decode (all types, various payload sizes) – 7 tests PASS
- [x] Unit: JWT generation + validation – 5 tests PASS
- [x] Unit: hub frame dispatch (real WS pair) – 6 tests PASS
- [x] Unit: PTY JSON round-trip – 2 tests PASS
- [~] Integration: full server round-trip (PTY tests need `cap_setuid/setgid` or root)

### Known skips / next steps
- SSH auth test: skipped (no local sshd in dev environment)
- PTY spawn tests: skipped (need `WEBDESKTOPD_TEST_PTY=1` + Linux capabilities)
- Stats frames (0x03): not yet implemented (Phase 3)
- File manager frames (0x04-0x09): not yet implemented (Phase 4)

---

## Phase 2 – Reconnection + Multi-tab ✅ COMPLETE

- [x] Multi-tab: open/close PTY via `0x0A`/`0x0B`, vertical tab list in frontend
- [x] Reconnect: on new WS, replay ring buffer tail → live stream
- [x] Session sync frame `0x0C` on reconnect (channel list + desktop state)

---

## Phase 3 – Stats Dock ✅ COMPLETE

- [x] `internal/stats` – `/proc` metrics collector (CPU, RAM, disk, net)
- [x] Ref-counted: starts on first UserSession, stops on last
- [x] Stats frame `0x03` on channel 0 every 1s
- [x] Frontend dock component (CPU/RAM/disk/net bars + kernel/uptime)

---

## Phase 4 – File Manager ✅ COMPLETE

- [x] List directory — `FrameFileList (0x04)` / `FrameFileListResp (0x05)`, response now wraps `{path, entries, error}`
- [x] Upload — `FrameFileUpload (0x06)`, chunked 64 KB, progress via `0x07`
- [x] Download — `FrameFileDownloadReq (0x08)` / `FrameFileDownload (0x09)`, chunk reassembly in browser
- [x] File ops `FrameFileOp (0x11)` — rename, delete (recursive `os.RemoveAll`), mkdir (recursive), touch, copy (file + dir tree)
- [x] `homeDir` added to `SessionSyncPayload` — looked up via `os/user`, falls back to `/home/<user>`
- [x] Frontend `FileManager.svelte` — breadcrumbs, grid icons by type (folder/image/video/audio/archive/code/pdf/file), show/hide hidden, new folder, new file, upload (button + drag-and-drop), download, copy, cut, paste, delete (with confirm), inline rename (F2), right-click context menu, multi-select (click/Ctrl/Shift), keyboard shortcuts
- [x] Dock — Files launcher button live; Files entry in running-windows strip with × close; `activeApp` state (`terminal` | `files`)
- [x] e2e — 29 tests pass against live build-server: list, metadata, homeDir sync, mkdir (flat + nested), touch, upload/download round-trip, upload 200 KB integrity, rename, copy file, copy dir, delete file, delete dir (recursive)

---

## Phase 5 – Desktop State + Port Proxy

- [ ] `internal/desktop` – read/write `~/.webdesktopd/state.json`
- [ ] Frames `0x12`/`0x13` for push/save
- [ ] Frontend: save window positions + wallpaper on change
- [ ] Port proxy: `0x0F`/`0x10`, TCP tunnel over WS channel
- [ ] Frontend: port proxy tab with Service Worker virtual host

---

## Phase 6 – Hardening

- [ ] Config file / env var loading (port, JWT secret, SSH addr, ring buffer cap)
- [ ] Structured logging (`log/slog`)
- [ ] Graceful shutdown (SIGTERM → drain sessions)
- [ ] Rate limiting on `/auth`
- [ ] Max file upload size enforcement
- [ ] `setcap` instructions in README

---

## Architecture Decisions

| Decision | Choice | Reason |
|---|---|---|
| WS library | `gorilla/websocket` | Stable, widely used |
| JWT library | `golang-jwt/jwt/v5` | Official successor |
| PTY library | `creack/pty` | Standard |
| Frontend framework | SvelteKit 5 + Bun | Fast DX, Bun for speed |
| CSS | TailwindCSS v4 | No config file, Vite plugin |
| Terminal | xterm.js (`@xterm/xterm`) | Proven, npm available |
| Frame encoding | big-endian binary: type(1)+chanID(2)+len(4)+payload | Minimal overhead |

---

## Session Notes

### Session 1 (2026-03-26)
- Created `architecture.md` (existing)
- Created `CLAUDE.md`, `ROADMAP.md`, `Makefile`
- Implemented full Phase 1: backend + frontend
- All unit tests passing (33 tests, race-clean) across auth/hub/pty/ringbuf/server
- SSH integration tests pass against build-server (127.0.0.1:32233, user abb)
- PTY fix: skip `Credential` when spawning as current user (`setgroups` requires CAP_SETGID)
- Full binary builds and serves embedded frontend (HTTP 200 on `/`)
- `cmd/deploy`: Go tool to cross-compile + SCP to remote + start server
- `cmd/tunnel`: Go SSH port-forward tunnel (password auth, gorilla/websocket)
- `e2e/`: 25 tests passing against live build-server (auth, PTY, files, reconnect)
- **Known issue resolved in Session 2**: Browser terminal was unresponsive

### Session 2 (2026-03-27)
- Fixed browser terminal (5 bugs):
  1. `server.go`: replaced `attachHub` with `registerHandlers` (no ring buffer replay on connect)
  2. `server.go`: `handleOpenPTY` now idempotent — re-attaches with ring buffer replay if PTY already open
  3. `client.ts`: `openPTY` now sends on chanID 0 (control channel) not PTY chanID
  4. `Terminal.svelte`: added `term.focus()`, moved `openPTY` call to AFTER handler registration
  5. `desktop/+page.svelte`: fixed session sync field (`ptyChannels` not `channels`), fixed `closeTerminal` to send on chanID 0
- e2e reconnect test updated: explicitly sends `openPTY` to trigger ring buffer replay
- e2e reconnect test: cleans up stale PTY at start to avoid inter-run contamination
- All 25 e2e tests pass

### Session 3 (2026-03-27)
- Implemented Phase 2 reconnection + multi-tab completion:
  - `session.svelte.ts`: added `connectCount` state (increments on every WS open)
  - `Terminal.svelte`: split single `$effect` into init effect (create xterm, register handler) and reconnect effect (send openPTY when connected+ready)
  - `desktop/+page.svelte`: passes `connectCount` prop to Terminal component
- Reconnect flow: WS auto-reconnects after 2s → `connectCount` increments → reconnect effect fires → `openPTY` re-sent → server re-attaches PTY and replays ring buffer → live streaming resumes
- Known limitation: ring buffer replay on reconnect shows output from ring buffer start (may include pre-disconnect content); deduplication deferred to Phase 6

### Session 4 (2026-03-27)
- Implemented Phase 3 stats dock (backend):
  - `internal/stats/collector.go`: ref-counted `Collector` reads `/proc/stat` (CPU), `/proc/meminfo` (RAM), `syscall.Statfs("/")` (disk), `/proc/net/dev` (net rates), `/proc/uptime`, `/proc/loadavg`, `syscall.Uname` (kernel), `os.Hostname`
  - Broadcasts `FrameStats` (0x03) on chanID 0 every 1s to all registered senders
  - Starts on first WS connect, stops when last client disconnects
  - `server.go`: added `stats.Collector` to `Server`; `handleWS` calls `Add`/`Remove` (deferred)
  - 3 unit tests pass (start/stop lifecycle, payload validation, multiple senders)
  - Frontend `StatsDock.svelte` was already complete (Phase 1)

### Session 5 (2026-03-27)
- Implemented Phase 4 file manager (full):
  - Backend: `fileops.go` — `mkdirPath`, `touchFile`, `copyPath`/`copyFile`/`copyDir` (recursive), `deleteFile` upgraded to `os.RemoveAll`
  - Backend: `server.go` — `FileListResponse` wraps `{path, entries, error}`; `handleFileOp` handles mkdir/touch/copy; `homeDir` in `SessionSyncPayload` (falls back to `/home/<user>` if `os/user.Lookup` fails)
  - Frontend: `FileManager.svelte` — full-featured file manager (see Phase 4 checklist)
  - Frontend: `session.svelte.ts` — added `homeDir`, `activeApp`, `fileManagerOpen`
  - Frontend: `Dock.svelte` — Files launcher live, running-window button with close
  - Frontend: `desktop/+page.svelte` — mounts FileManager, routes session sync homeDir
  - e2e: `client_test.go` — `listDir` updated for new response format; `syncSession` returns `sessionSyncResult{PTYChannels, HomeDir}`; added `fileOp`, `uploadFile`, `downloadFile` helpers
  - e2e: `files_test.go` — 16 new tests; all 29 file e2e tests pass against live build-server
  - e2e: `pty_test.go` — `requirePTY` skips PTY tests when user not in local passwd
  - Deployment: `go run ./cmd/deploy --pass='max***'` → builds, SCPs, starts on build-server:18080
  - Tunnel: `go run ./cmd/tunnel --pass='max***'` → localhost:19080 → remote:18080
  - Full e2e run: 35 PASS, 6 SKIP (PTY), 0 FAIL
