package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestAuthValidCredentials verifies that correct credentials return a JWT.
func TestAuthValidCredentials(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"username": cfg.User,
		"password": cfg.Pass,
	})
	resp, err := http.Post(cfg.BaseURL+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var ar authResponse
	json.NewDecoder(resp.Body).Decode(&ar) //nolint:errcheck
	if ar.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if len(strings.Split(ar.Token, ".")) != 3 {
		t.Fatalf("response does not look like a JWT: %q", ar.Token)
	}
	t.Logf("JWT: %s…", ar.Token[:min(50, len(ar.Token))])
}

// TestAuthInvalidPassword verifies that a wrong password returns 401.
func TestAuthInvalidPassword(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"username": cfg.User,
		"password": "definitelywrong___xyz999",
	})
	resp, err := http.Post(cfg.BaseURL+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestAuthEmptyUsername verifies that an empty username returns 400.
func TestAuthEmptyUsername(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"username": "", "password": "anything"})
	resp, err := http.Post(cfg.BaseURL+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestAuthMissingPassword verifies that omitting password and key returns 400.
func TestAuthMissingPassword(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"username": cfg.User})
	resp, err := http.Post(cfg.BaseURL+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestAuthInvalidJSON verifies that malformed JSON returns 400.
func TestAuthInvalidJSON(t *testing.T) {
	resp, err := http.Post(cfg.BaseURL+"/auth", "application/json",
		bytes.NewBufferString("{not json}"))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestHealthEndpoint verifies the /health endpoint.
func TestHealthEndpoint(t *testing.T) {
	resp, err := http.Get(cfg.BaseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body) //nolint:errcheck
	if body["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", body)
	}
}

// TestWSInvalidToken verifies that an invalid JWT is rejected at the WS upgrade.
func TestWSInvalidToken(t *testing.T) {
	url := cfg.WSURL + "/ws?token=invalid.token.value"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected dial error for invalid token, got nil")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestWSMissingToken verifies that a missing token is rejected at WS upgrade.
func TestWSMissingToken(t *testing.T) {
	url := cfg.WSURL + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected dial error for missing token, got nil")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestWSValidTokenConnects verifies that a valid JWT allows WS connection
// and that a session-sync frame is received.
func TestWSValidTokenConnects(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	channels := c.syncSession(2 * time.Second)
	t.Logf("session-sync received: %d open PTY channel(s)", len(channels))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
