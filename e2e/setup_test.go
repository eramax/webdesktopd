// Package e2e contains end-to-end tests for webdesktopd.
//
// Tests run against a live server. Two modes are supported:
//
//  1. External server (default): set WEBDESKTOPD_URL to point at a running
//     instance. If unset, defaults to http://localhost:19080.
//
//  2. Embedded server: if WEBDESKTOPD_URL is empty and WEBDESKTOPD_SSH_ADDR is
//     set, a server is started in-process via httptest. This is useful for local
//     development without a separate deploy step.
//
// Required env vars:
//
//	WEBDESKTOPD_USER   username to authenticate with (default: abb)
//	WEBDESKTOPD_PASS   password  (required; tests skip if not set)
//
// Optional env vars:
//
//	WEBDESKTOPD_URL      base HTTP URL of a running server (default: http://localhost:19080)
//	WEBDESKTOPD_SSH_ADDR sshd address used when starting an embedded server (e.g. 127.0.0.1:32233)
package e2e

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"webdesktopd/internal/server"
)

// cfg holds resolved test configuration.
var cfg testConfig

type testConfig struct {
	BaseURL  string // e.g. http://localhost:19080
	WSURL    string // e.g. ws://localhost:19080
	User     string
	Pass     string
	embedded *httptest.Server // non-nil when we started the server ourselves
}

func TestMain(m *testing.M) {
	cfg = resolveConfig()
	if cfg.Pass == "" {
		fmt.Fprintln(os.Stderr, "e2e: WEBDESKTOPD_PASS not set — skipping all e2e tests")
		os.Exit(0)
	}
	code := m.Run()
	if cfg.embedded != nil {
		cfg.embedded.Close()
	}
	os.Exit(code)
}

func resolveConfig() testConfig {
	user := envOr("WEBDESKTOPD_USER", "abb")
	pass := os.Getenv("WEBDESKTOPD_PASS")
	baseURL := os.Getenv("WEBDESKTOPD_URL")

	if baseURL != "" {
		// Use explicitly provided external server.
		return testConfig{
			BaseURL: strings.TrimRight(baseURL, "/"),
			WSURL:   toWS(baseURL),
			User:    user,
			Pass:    pass,
		}
	}

	sshAddr := os.Getenv("WEBDESKTOPD_SSH_ADDR")
	if sshAddr != "" {
		// Start an embedded server backed by a real sshd.
		srv := server.New(server.Config{
			JWTSecret: []byte("e2e-test-secret"),
			SSHAddr:   sshAddr,
		})
		ts := httptest.NewServer(srv.Handler())
		return testConfig{
			BaseURL:  ts.URL,
			WSURL:    toWS(ts.URL),
			User:     user,
			Pass:     pass,
			embedded: ts,
		}
	}

	// Default: assume a server is reachable at localhost:19080 (e.g. via SSH tunnel).
	return testConfig{
		BaseURL: "http://localhost:19080",
		WSURL:   "ws://localhost:19080",
		User:    user,
		Pass:    pass,
	}
}

func toWS(httpURL string) string {
	return strings.Replace(strings.Replace(httpURL, "https://", "wss://", 1), "http://", "ws://", 1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
