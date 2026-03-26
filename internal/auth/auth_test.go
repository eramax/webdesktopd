package auth

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func newTestAuth(secret []byte, ttl time.Duration) *Authenticator {
	return New(DefaultSSHAddr, secret, ttl)
}

// TestValidateTokenRoundTrip verifies that a freshly issued token returns the correct username.
func TestValidateTokenRoundTrip(t *testing.T) {
	a := newTestAuth([]byte("test-secret-key-32bytes-padding!!"), DefaultJWTTTL)
	token, err := a.issueJWT("alice")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	username, err := a.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if username != "alice" {
		t.Fatalf("expected username %q, got %q", "alice", username)
	}
}

// TestValidateTokenExpired verifies that an expired token returns an error.
func TestValidateTokenExpired(t *testing.T) {
	a := newTestAuth([]byte("test-secret-key"), 1*time.Millisecond)
	token, err := a.issueJWT("bob")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	// Wait for expiry.
	time.Sleep(10 * time.Millisecond)
	_, err = a.ValidateToken(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

// TestValidateTokenTamperedSignature verifies that a tampered token is rejected.
func TestValidateTokenTamperedSignature(t *testing.T) {
	a := newTestAuth([]byte("test-secret-key"), DefaultJWTTTL)
	token, err := a.issueJWT("carol")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	// Append garbage to tamper with the signature.
	tampered := token + "tampered"
	_, err = a.ValidateToken(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

// TestValidateTokenWrongSecret verifies that a token signed with a different secret is rejected.
func TestValidateTokenWrongSecret(t *testing.T) {
	signer := newTestAuth([]byte("original-secret"), DefaultJWTTTL)
	validator := newTestAuth([]byte("different-secret"), DefaultJWTTTL)

	token, err := signer.issueJWT("dave")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	_, err = validator.ValidateToken(token)
	if err == nil {
		t.Fatal("expected error when validating with wrong secret, got nil")
	}
}

// TestValidateTokenMalformed verifies that a garbage string returns an error.
func TestValidateTokenMalformed(t *testing.T) {
	a := newTestAuth([]byte("test-secret"), DefaultJWTTTL)
	_, err := a.ValidateToken("not.a.jwt")
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
}

// TestAuthenticate is an integration test that requires a local sshd.
func TestAuthenticate(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "localhost:22", time.Second)
	if err != nil {
		t.Skip("no local sshd available")
	}
	conn.Close()

	// Verify that a wrong password returns an error.
	a := newTestAuth([]byte("test-secret"), DefaultJWTTTL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = a.Authenticate(ctx, "nonexistentuser12345", "wrongpassword")
	if err == nil {
		t.Fatal("expected error for invalid credentials, got nil")
	}
}

// TestAuthenticateRealSSH is a full integration test against a real sshd.
// Set WEBDESKTOPD_TEST_SSH_ADDR, WEBDESKTOPD_TEST_SSH_USER, WEBDESKTOPD_TEST_SSH_PASS to enable.
func TestAuthenticateRealSSH(t *testing.T) {
	addr := requireEnv(t, "WEBDESKTOPD_TEST_SSH_ADDR")
	user := requireEnv(t, "WEBDESKTOPD_TEST_SSH_USER")
	pass := requireEnv(t, "WEBDESKTOPD_TEST_SSH_PASS")

	a := New(addr, []byte("integration-test-secret"), DefaultJWTTTL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Valid credentials → JWT issued.
	token, err := a.Authenticate(ctx, user, pass)
	if err != nil {
		t.Fatalf("Authenticate with valid creds: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	// JWT must round-trip to correct username.
	gotUser, err := a.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if gotUser != user {
		t.Fatalf("expected username %q, got %q", user, gotUser)
	}
	t.Logf("JWT issued for %q: %s…", user, token[:min(40, len(token))])

	// Wrong password must be rejected.
	_, err = a.Authenticate(ctx, user, "definitelywrongpassword999")
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
	t.Log("wrong password correctly rejected")
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
