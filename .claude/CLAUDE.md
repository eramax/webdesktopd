# Project Memory

This file is auto-loaded by Claude Code at the start of every session.

---

## e2e Test Environment

SSH host alias: `build-server` (`~/.ssh/config`)
- HostName: `127.0.0.1`, Port: `32233`, User: `abb`, Password: `max***`

User `abb` exists on the remote server but NOT in local `/etc/passwd` — PTY tests skip locally (expected).

---

## Post-Implementation Workflow

After **every** implementation (including small fixes), always complete this sequence before considering the task done:

1. **Build frontend** — `cd frontend && npm run build`
2. **Deploy to remote** — `go run ./cmd/deploy --pass='max***'` (builds binary, SCPs to build-server, starts on port 18080)
3. **Open tunnel** — `go run ./cmd/tunnel --pass='max***' --local=19080 --remote=18080 &` (skip if port 19080 already in use — tunnel is already up)
4. **Run e2e tests**:
   ```bash
   WEBDESKTOPD_URL=http://localhost:19080 WEBDESKTOPD_SSH_ADDR=127.0.0.1:32233 \
     WEBDESKTOPD_PASS='max***' go test ./e2e/... -timeout 120s
   ```
   - `WEBDESKTOPD_SSH_ADDR` is required for proxy/bun tests (`proxy_bun_test.go`, `proxy_http_test.go`)
   - Without `WEBDESKTOPD_URL`, setup starts an embedded local server that cannot reach the remote bun process — proxy tests fail
5. **Update ROADMAP.md** — mark completed phases `[x]`, append a Session N notes block with: what was implemented, bugs fixed, decisions made, test results

**Never skip build+deploy, not even for one-line changes. Always use `ROADMAP.md` (uppercase), never create a new file.**

---

## Notes

- The progress log is `ROADMAP.md` (uppercase, in project root) — not `architecture.md`.
- All 29 non-PTY e2e tests pass against the live server.
