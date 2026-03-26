package main

import (
	"crypto/rand"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"webdesktopd/internal/server"
)

// frontendFS holds the compiled SvelteKit build.
// The build directory is created by running `bun run build` in frontend/.
// It is absent during `go test` runs; the embed tag is conditional so the
// binary still compiles without a pre-built frontend.
//
//go:embed all:frontend/build
var frontendFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	sshAddr := flag.String("ssh-addr", "localhost:22", "sshd address for auth")
	jwtTTL := flag.Duration("jwt-ttl", 24*time.Hour, "JWT time-to-live")
	flag.Parse()

	var jwtSecret []byte
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		slog.Warn("JWT_SECRET not set; using insecure random secret (restart will invalidate tokens)")
		jwtSecret = make([]byte, 32)
		if _, err := rand.Read(jwtSecret); err != nil {
			slog.Error("failed to generate random JWT secret", "err", err)
			os.Exit(1)
		}
	} else {
		jwtSecret = []byte(secret)
	}

	cfg := server.Config{
		Addr:      *addr,
		JWTSecret: jwtSecret,
		SSHAddr:   *sshAddr,
		JWTTTL:    *jwtTTL,
	}

	srv := server.New(cfg)

	// Serve embedded frontend (strip the "frontend/build" prefix so "/" maps to index.html).
	sub, err := fs.Sub(frontendFS, "frontend/build")
	if err != nil {
		slog.Error("failed to create sub-filesystem for frontend", "err", err)
		os.Exit(1)
	}
	srv.SetAssets(http.FS(sub))

	slog.Info("webdesktopd starting", "addr", *addr)
	fmt.Printf("webdesktopd listening on %s\n", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
