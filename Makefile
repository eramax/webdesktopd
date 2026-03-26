.PHONY: all build frontend backend test clean run dev-backend dev-frontend

all: build

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

clean:
	rm -f webdesktopd
	rm -rf frontend/build frontend/.svelte-kit
