.PHONY: all build frontend backend test clean run dev-backend dev-frontend deploy tunnel

all: build

HOST ?= 127.0.0.1
SSH_PORT ?= 32233
REMOTE_USER ?= abb
REMOTE_PORT ?= 18080
LOCAL_PORT ?= 19080
PASS ?=

# Full production build: frontend then embed into Go binary
build: frontend backend

# Build the SvelteKit frontend into frontend/build/
frontend:
	cd frontend && bun install --frozen-lockfile && bun run build

# Build the Go binary (requires frontend/build/ to exist)
backend:
	go build -o webdesktopd .

# Run all unit tests (excludes frontend/ directory)
test:
	go test -race webdesktopd/internal/...

# Run e2e tests against a live server.
# Required: WEBDESKTOPD_PASS
# Optional: WEBDESKTOPD_URL (default http://localhost:19080)
#           WEBDESKTOPD_USER (default abb)
#           WEBDESKTOPD_SSH_ADDR (starts embedded server if URL not set)
e2e:
	go test -v -timeout 120s webdesktopd/e2e

# Run unit + e2e tests together.
test-all: test e2e

# Start the server (frontend must be built first)
run: build
	JWT_SECRET=$${JWT_SECRET:-dev-secret} ./webdesktopd

# Development mode: run backend only (no embedded frontend)
# Frontend is served separately via `make dev-frontend`
dev-backend:
	JWT_SECRET=$${JWT_SECRET:-dev-secret} go run . --addr :8080

# Development mode: run Vite dev server (proxies /auth and /ws to :8080)
dev-frontend:
	cd frontend && bun run dev

# Deploy the current build to the remote host.
deploy: build
	@test -n "$(PASS)" || (echo "PASS is required: make deploy PASS=..." && exit 1)
	go run ./cmd/deploy --host "$(HOST)" --port "$(SSH_PORT)" --user "$(REMOTE_USER)" --pass "$(PASS)" --remote-port "$(REMOTE_PORT)"

# Open a local SSH tunnel to the deployed remote server.
tunnel:
	@test -n "$(PASS)" || (echo "PASS is required: make tunnel PASS=..." && exit 1)
	go run ./cmd/tunnel --host "$(HOST)" --port "$(SSH_PORT)" --user "$(REMOTE_USER)" --pass "$(PASS)" --local "$(LOCAL_PORT)" --remote "$(REMOTE_PORT)"

clean:
	rm -f webdesktopd
	rm -rf frontend/build frontend/.svelte-kit
