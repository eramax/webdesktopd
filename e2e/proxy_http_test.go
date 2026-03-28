package e2e

import (
	"encoding/json"
	"fmt"
	"io"
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

// TestHTTPProxyRefreshBunRoot verifies that refreshing a proxied page returns
// the same content on repeated requests.
func TestHTTPProxyRefreshBunRoot(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)

	first := proxyGet(t, token, srv.port, "/")
	body1, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first GET: expected 200, got %d", first.StatusCode)
	}

	second := proxyGet(t, token, srv.port, "/")
	body2, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second GET: expected 200, got %d", second.StatusCode)
	}

	if !strings.Contains(string(body1), "proxy-test-ok") || !strings.Contains(string(body2), "proxy-test-ok") {
		t.Fatalf("expected proxy-test-ok in both responses:\nfirst: %.300s\nsecond: %.300s", body1, body2)
	}
	t.Log("✓ proxied bun root remains loadable across refreshes")
}

// TestHTTPProxyDirectBunRootLoad verifies that a direct navigation to the
// proxied root works without depending on the desktop websocket bridge.
// This matches the browser behavior for opening a port in a new tab.
func TestHTTPProxyDirectBunRootLoad(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)

	resp := proxyGet(t, token, srv.port, "/")
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "proxy-test-ok") {
		t.Fatalf("expected proxy-test-ok in direct load, got:\n%.300s", body)
	}
	t.Log("✓ proxied bun root loads directly without the desktop bridge")
}

// TestHTTPProxyBareMountRootLoad verifies that the bare mount root without a
// trailing slash also routes to the upstream app.
func TestHTTPProxyBareMountRootLoad(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	u := fmt.Sprintf("%s/_proxy/%d", cfg.BaseURL, srv.port)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "wdd_token", Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "proxy-test-ok") {
		t.Fatalf("expected proxy-test-ok in bare mount response, got:\n%.300s", body)
	}
	t.Log("✓ bare proxy mount root loads successfully")
}

// TestHTTPProxyInjectsBaseHref verifies that HTML responses are rewritten with
// a proxy-local base href so relative links resolve inside the mount path.
func TestHTTPProxyInjectsBaseHref(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	resp := proxyGet(t, token, srv.port, "/relative")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf(`<base href="/_proxy/%d/">`, srv.port)
	if !strings.Contains(string(body), want) {
		t.Fatalf("expected rewritten base href %q, got:\n%.300s", want, body)
	}
	t.Log("✓ proxy injects proxy-local base href into HTML responses")
}

// TestHTTPProxyScopesCookiesToMountPath verifies that Set-Cookie headers are
// rewritten so browser cookies stay scoped to the proxy mount path.
func TestHTTPProxyScopesCookiesToMountPath(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	resp := proxyGet(t, token, srv.port, "/set-cookie")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("expected Set-Cookie header from upstream")
	}
	wantPath := fmt.Sprintf("Path=/_proxy/%d", srv.port)
	if !strings.Contains(cookies[0], wantPath) {
		t.Fatalf("expected rewritten cookie path %q, got %q", wantPath, cookies[0])
	}
	t.Log("✓ proxy scopes cookies to the mount path")
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
