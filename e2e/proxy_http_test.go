package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// proxyGet makes an authenticated HTTP GET through the REST proxy endpoint
// to path on the given port. Caller must close resp.Body.
func proxyGet(t *testing.T, token string, port int, path string) *http.Response {
	t.Helper()
	u := fmt.Sprintf("%s/_proxy/%d%s", cfg.BaseURL, port, path)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("proxyGet: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "wdd_token", Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxyGet %s: %v", u, err)
	}
	return resp
}

// TestHTTPProxyStripsXFrameOptions verifies that X-Frame-Options set by the
// upstream is removed so the proxied app can be embedded in an iframe.
func TestHTTPProxyStripsXFrameOptions(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	resp := proxyGet(t, token, srv.port, "/xfo")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if xfo := resp.Header.Get("X-Frame-Options"); xfo != "" {
		t.Errorf("X-Frame-Options should be stripped, got %q", xfo)
	}
	t.Log("✓ X-Frame-Options stripped by HTTP proxy")
}

// TestHTTPProxyStripsCSPFrameAncestors verifies that the frame-ancestors
// directive is removed from Content-Security-Policy while other directives
// (e.g. default-src) are preserved.
func TestHTTPProxyStripsCSPFrameAncestors(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	resp := proxyGet(t, token, srv.port, "/csp")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "frame-ancestors") {
		t.Errorf("frame-ancestors should be stripped from CSP, got %q", csp)
	}
	if csp != "" && !strings.Contains(csp, "default-src") {
		t.Errorf("expected default-src to be preserved in CSP, got %q", csp)
	}
	t.Logf("✓ CSP frame-ancestors stripped; remaining: %q", csp)
}

// TestHTTPProxyStripsWddToken verifies that the internal wdd_token auth cookie
// is NOT forwarded to the upstream application, but other cookies are.
func TestHTTPProxyStripsWddToken(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)

	u := fmt.Sprintf("%s/_proxy/%d/cookies", cfg.BaseURL, srv.port)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "wdd_token", Value: token})
	req.AddCookie(&http.Cookie{Name: "my_app_session", Value: "abc123"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Cookies string `json:"cookies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	t.Logf("cookies seen by upstream: %q", body.Cookies)

	if strings.Contains(body.Cookies, "wdd_token") {
		t.Errorf("wdd_token must not be forwarded to upstream, got: %q", body.Cookies)
	}
	if !strings.Contains(body.Cookies, "my_app_session=abc123") {
		t.Errorf("expected my_app_session to pass through, got: %q", body.Cookies)
	}
	t.Log("✓ wdd_token stripped; other cookies forwarded to upstream")
}

// TestHTTPProxyWebSocketRelay verifies that WebSocket upgrade requests are
// relayed end-to-end through the HTTP proxy to the upstream WS server.
// The upstream bun echo server sends back whatever message it receives.
func TestHTTPProxyWebSocketRelay(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)

	wsURL := fmt.Sprintf("%s/_proxy/%d/ws", cfg.WSURL, srv.port)
	t.Logf("dialing WS proxy: %s", wsURL)

	header := http.Header{}
	header.Set("Cookie", "wdd_token="+token)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial WS proxy: %v", err)
	}
	defer conn.Close()

	const msg = "proxy-ws-echo-test"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write WS message: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read WS message: %v", err)
	}
	if string(got) != msg {
		t.Errorf("expected echo %q, got %q", msg, string(got))
	}
	t.Logf("✓ WebSocket message relayed and echoed through proxy: %q", string(got))
}
