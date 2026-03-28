package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseJWTSecretEnv(t *testing.T) {
	content := strings.Join([]string{
		"# comment",
		"SSH_ADDR=localhost:22",
		"JWT_SECRET=abc123",
		"WEBDESKTOPD_ADDR=:8080",
	}, "\n")

	if got := parseJWTSecretEnv(content); got != "abc123" {
		t.Fatalf("parseJWTSecretEnv() = %q, want %q", got, "abc123")
	}
}

func TestGenerateJWTSecret(t *testing.T) {
	secret, err := generateJWTSecret()
	if err != nil {
		t.Fatalf("generateJWTSecret() error = %v", err)
	}
	if len(secret) != 64 {
		t.Fatalf("generateJWTSecret() length = %d, want 64", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Fatalf("generateJWTSecret() returned non-hex secret %q: %v", secret, err)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("ab'cd"); got != "'ab'\"'\"'cd'" {
		t.Fatalf("shellQuote() = %q, want %q", got, "'ab'\"'\"'cd'")
	}
}
