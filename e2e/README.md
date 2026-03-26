# e2e tests

End-to-end tests for `webdesktopd`. They run against a live server instance over real HTTP and WebSocket connections.

## Running

```bash
# Against an already-running server (e.g. via SSH tunnel on :19080):
WEBDESKTOPD_URL=http://localhost:19080 \
WEBDESKTOPD_USER=abb \
WEBDESKTOPD_PASS='secret' \
go test -v -timeout 120s webdesktopd/e2e

# Or via Makefile (same env vars):
make e2e
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `WEBDESKTOPD_PASS` | — | **Required.** Password for the test user. |
| `WEBDESKTOPD_USER` | `abb` | Username to authenticate with. |
| `WEBDESKTOPD_URL` | `http://localhost:19080` | Base URL of a running server. |
| `WEBDESKTOPD_SSH_ADDR` | — | If `WEBDESKTOPD_URL` is not set, starts an embedded test server using this sshd address (e.g. `127.0.0.1:32233`). |

## Test files

| File | What it tests |
|---|---|
| `auth_test.go` | `POST /auth` (valid/invalid creds), WS JWT validation, `/health` |
| `pty_test.go` | Open PTY, echo, multi-tab, ring-buffer reconnect, resize, session-sync |
| `files_test.go` | Directory listing: home, /tmp, /etc, metadata, non-existent, IsDir |

## Adding tests for a new feature

1. Create `e2e/<feature>_test.go` with `package e2e`.
2. Use `mustAuth(t, cfg.User, cfg.Pass)` to get a JWT.
3. Use `dial(t, token)` to open a WebSocket (`WSClient`).
4. Use the helpers in `client_test.go`:
   - `c.openPTY(chanID, shell, cwd)` — open a terminal
   - `c.sendInput(chanID, text)` — write to PTY
   - `c.waitForOutput(chanID, marker, timeout)` — wait for output
   - `c.listDir(path, timeout)` — file list (returns `[]FileInfo`)
   - `c.send(type, chanID, payload)` / `c.sendJSON(...)` — raw frames
   - `c.subscribe(chanID)` / `c.unsubscribe(...)` — raw frame subscription
5. Run `make e2e` and ensure all existing tests still pass.

## Server deploy (build-server)

```bash
# Build + deploy to 127.0.0.1:32233 as abb, start on :18080
go run webdesktopd/cmd/deploy --pass='secret'

# Open SSH tunnel so localhost:19080 → build-server:18080
go run webdesktopd/cmd/tunnel --pass='secret' --local=19080 --remote=18080
```
