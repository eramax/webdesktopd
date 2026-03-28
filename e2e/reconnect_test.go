package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"webdesktopd/internal/server"
)

func TestTokenSurvivesRestartWithStableJWTSecret(t *testing.T) {
	if cfg.SSHAddr == "" {
		t.Skip("requires WEBDESKTOPD_SSH_ADDR so the embedded server can authenticate users")
	}

	const jwtSecret = "e2e-stable-jwt-secret"

	srv1 := server.New(server.Config{
		JWTSecret: []byte(jwtSecret),
		SSHAddr:   cfg.SSHAddr,
	})
	token := mustAuthOnHandler(t, srv1.Handler(), cfg.User, cfg.Pass)
	assertValidateOK(t, srv1.Handler(), token)

	srv2 := server.New(server.Config{
		JWTSecret: []byte(jwtSecret),
		SSHAddr:   cfg.SSHAddr,
	})
	assertValidateOK(t, srv2.Handler(), token)
}

func mustAuthOnHandler(t *testing.T, h http.Handler, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /auth: got HTTP %d", rec.Code)
	}

	var ar authResponse
	if err := json.NewDecoder(rec.Body).Decode(&ar); err != nil {
		t.Fatalf("decode /auth response: %v", err)
	}
	if ar.Token == "" {
		t.Fatal("expected non-empty token")
	}
	return ar.Token
}

func assertValidateOK(t *testing.T, h http.Handler, token string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/validate?token="+token, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /validate: got HTTP %d", rec.Code)
	}
}
