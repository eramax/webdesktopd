package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"webdesktopd/internal/server"
)

func testServer(t *testing.T) (*server.Server, *httptest.Server) {
	t.Helper()
	sshAddr := os.Getenv("WEBDESKTOPD_TEST_SSH_ADDR")
	if sshAddr == "" {
		sshAddr = "localhost:22"
	}
	cfg := server.Config{
		JWTSecret: []byte("server-test-secret"),
		SSHAddr:   sshAddr,
		JWTTTL:    time.Hour,
	}
	srv := server.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// TestHealthEndpoint verifies /health returns 200.
func TestHealthEndpoint(t *testing.T) {
	_, ts := testServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", body)
	}
}

// TestAuthEndpointInvalidJSON verifies /auth rejects malformed JSON.
func TestAuthEndpointInvalidJSON(t *testing.T) {
	_, ts := testServer(t)
	resp, err := http.Post(ts.URL+"/auth", "application/json", bytes.NewBufferString("not json"))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestAuthEndpointMissingFields verifies /auth rejects empty credentials.
func TestAuthEndpointMissingFields(t *testing.T) {
	_, ts := testServer(t)
	body := `{"username":"","password":""}`
	resp, err := http.Post(ts.URL+"/auth", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty credentials, got %d", resp.StatusCode)
	}
}

// TestAuthEndpointRealSSH tests the full /auth flow against a real sshd.
// Set WEBDESKTOPD_TEST_SSH_ADDR, WEBDESKTOPD_TEST_SSH_USER, WEBDESKTOPD_TEST_SSH_PASS to enable.
func TestAuthEndpointRealSSH(t *testing.T) {
	sshAddr := os.Getenv("WEBDESKTOPD_TEST_SSH_ADDR")
	user := os.Getenv("WEBDESKTOPD_TEST_SSH_USER")
	pass := os.Getenv("WEBDESKTOPD_TEST_SSH_PASS")
	if sshAddr == "" || user == "" || pass == "" {
		t.Skip("WEBDESKTOPD_TEST_SSH_ADDR/USER/PASS not set")
	}

	_, ts := testServer(t)

	// --- Valid credentials ---
	reqBody, _ := json.Marshal(map[string]string{
		"username": user,
		"password": pass,
	})
	resp, err := http.Post(ts.URL+"/auth", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var authResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	token := authResp["token"]
	if token == "" {
		t.Fatalf("expected token in response, got %v", authResp)
	}
	t.Logf("✓ /auth issued JWT for %q: %s…", user, token[:min(40, len(token))])

	// --- Wrong password ---
	badBody, _ := json.Marshal(map[string]string{
		"username": user,
		"password": "definitelywrong999",
	})
	resp2, err := http.Post(ts.URL+"/auth", "application/json", bytes.NewReader(badBody))
	if err != nil {
		t.Fatalf("POST /auth (bad pass): %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	t.Log("✓ wrong password correctly returns 401")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
