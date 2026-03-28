# Repository Guidelines

## Project Structure & Module Organization
This repository is a Go daemon with a SvelteKit frontend. Core backend code lives under `internal/`:
`auth/`, `hub/`, `pty/`, `ringbuf/`, `server/`, and `stats/`. CLI entry points are in `cmd/` (`deploy` and `tunnel`).
The web UI is in `frontend/src/`, with routes in `frontend/src/routes/`, reusable UI in `frontend/src/lib/components/`, and shared client/protocol code in `frontend/src/lib/`. End-to-end tests live in `e2e/`. Build output is generated into `frontend/build/` and should not be edited by hand.

## Build, Test, and Development Commands
- `make build` - builds the Svelte frontend, then compiles the Go daemon.
- `make run` - builds and starts the daemon with a dev JWT secret.
- `make dev-backend` - runs the Go server on `:8080` without embedded frontend assets.
- `make dev-frontend` - starts the Vite dev server on `:5173`.
- `make test` - runs Go unit tests under `internal/...` with the race detector.
- `make e2e` - runs browser/server integration tests in `e2e/` and requires `WEBDESKTOPD_PASS`.
- `cd frontend && bun run check` - runs Svelte/TypeScript validation.

## Coding Style & Naming Conventions
Use `gofmt` for Go code and keep package names short, lower-case, and domain-focused. Follow existing Go error handling and keep exported identifiers descriptive. In the frontend, use the established Svelte 5 patterns, two-space indentation, camelCase for variables/functions, and PascalCase for components such as `Terminal.svelte` or `StatsDock.svelte`. Use `bun` for frontend dependency and build tasks.

## Testing Guidelines
Prefer table-driven Go tests in `*_test.go` files. Unit tests should stay close to the package they cover; use e2e tests when the behavior spans auth, WebSocket framing, PTY sessions, file operations, or browser UI. Keep test names specific to the behavior under test, and run `make test` plus `make e2e` before merging changes that affect behavior.

## Commit & Pull Request Guidelines
Recent history uses short imperative messages, often with a `fix:` or `chore:` prefix when appropriate. Keep commits focused and describe the effect. PRs should include a brief summary, testing notes, and screenshots or screen recordings for frontend changes. Link related issues when available and mention any environment variables or manual steps needed to reproduce.

## Configuration & Safety Notes
This daemon is Linux-focused and depends on SSH, PTYs, `/proc`, and `/sys`. Do not commit secrets, local state files, or generated assets. For local development, `frontend/vite.config.ts` proxies `/auth` and `/ws` to `localhost:8080`, so keep the backend running when testing the UI.
