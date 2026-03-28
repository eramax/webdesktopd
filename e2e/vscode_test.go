package e2e

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

const vsCodePort = 8080

// readVSCodePassword returns the code-server password.
// The remote setup uses the same password as SSH.
func readVSCodePassword(t *testing.T) string {
	t.Helper()
	if cfg.Pass == "" {
		t.Skip("WEBDESKTOPD_PASS not set")
	}
	return cfg.Pass
}

// vsCodeClient builds an http.Client with a cookie jar pre-seeded with the
// wdd_token so that all proxy requests are authenticated automatically,
// including ones made while following redirects.
func vsCodeClient(t *testing.T, token string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	baseU, _ := url.Parse(cfg.BaseURL)
	jar.SetCookies(baseU, []*http.Cookie{
		{Name: "wdd_token", Value: token, Path: "/"},
	})
	return &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
	}
}

// requireVSCode checks that code-server is reachable on vsCodePort via the
// proxy. Skips the test (rather than failing) when it is not running.
func requireVSCode(t *testing.T, client *http.Client) {
	t.Helper()
	u := fmt.Sprintf("%s/_proxy/%d/login", cfg.BaseURL, vsCodePort)
	resp, err := client.Get(u)
	if err != nil {
		t.Skipf("code-server proxy unreachable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusBadGateway {
		t.Skipf("code-server not running on port %d (502 from proxy)", vsCodePort)
	}
}

// TestVSCodeLoginPageLoads verifies that the code-server login page is
// accessible through the HTTP proxy without authenticating to code-server.
func TestVSCodeLoginPageLoads(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	client := vsCodeClient(t, token)
	requireVSCode(t, client)

	resp, err := client.Get(fmt.Sprintf("%s/_proxy/%d/login", cfg.BaseURL, vsCodePort))
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(bodyStr, "code-server") {
		t.Errorf("expected 'code-server' in login page, got:\n%.500s", bodyStr)
	}
	if !strings.Contains(bodyStr, `name="password"`) {
		t.Errorf("expected password field in login form, got:\n%.500s", bodyStr)
	}
	// X-Frame-Options must be absent so the page can load in an iframe.
	if xfo := resp.Header.Get("X-Frame-Options"); xfo != "" {
		t.Errorf("X-Frame-Options should be stripped by proxy, got %q", xfo)
	}
	t.Log("✓ code-server login page loaded via proxy")
}

// TestVSCodeLoginSucceeds logs in to code-server through the proxy and
// verifies that the workbench page loads after the redirect chain completes.
func TestVSCodeLoginSucceeds(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	client := vsCodeClient(t, token)
	requireVSCode(t, client)

	vsPassword := readVSCodePassword(t)
	loginURL := fmt.Sprintf("%s/_proxy/%d/login", cfg.BaseURL, vsCodePort)

	// POST the login form. http.Client follows the redirect chain automatically,
	// carrying the code-server-session cookie via the jar.
	resp, err := client.PostForm(loginURL, url.Values{
		"password": {vsPassword},
		"base":     {"."},
		// href tells code-server the full URL so it sets the cookie Path correctly.
		"href": {loginURL},
	})
	if err != nil {
		t.Fatalf("POST login: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	t.Logf("final URL after redirects: %s (status %d)", resp.Request.URL, resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login redirect, got %d\nbody: %.500s", resp.StatusCode, bodyStr)
	}

	// The workbench page must contain these code-server markers.
	if !strings.Contains(bodyStr, "codeServerVersion") {
		t.Errorf("expected 'codeServerVersion' in workbench page — login may have failed\nbody: %.500s", bodyStr)
	}
	if !strings.Contains(bodyStr, "workbench.js") {
		t.Errorf("expected 'workbench.js' reference in workbench page\nbody: %.500s", bodyStr)
	}

	// Session cookie must have been set and scoped to the proxy path.
	// Query the jar at the proxy path since code-server sets Path=/_proxy/{port}.
	proxyU, _ := url.Parse(fmt.Sprintf("%s/_proxy/%d/", cfg.BaseURL, vsCodePort))
	cookies := client.Jar.Cookies(proxyU)
	var hasSession bool
	for _, c := range cookies {
		if c.Name == "code-server-session" {
			hasSession = true
			break
		}
	}
	if !hasSession {
		t.Error("expected code-server-session cookie in jar after login")
	}

	t.Log("✓ code-server login succeeded; workbench page loaded via proxy")
}

// TestVSCodeWorkbenchRefreshAfterLogin verifies that reloading the workbench
// after a successful login keeps the session alive.
func TestVSCodeWorkbenchRefreshAfterLogin(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	client := vsCodeClient(t, token)
	requireVSCode(t, client)

	vsPassword := readVSCodePassword(t)
	loginURL := fmt.Sprintf("%s/_proxy/%d/login", cfg.BaseURL, vsCodePort)

	resp, err := client.PostForm(loginURL, url.Values{
		"password": {vsPassword},
		"base":     {"."},
		"href":     {loginURL},
	})
	if err != nil {
		t.Fatalf("POST login: %v", err)
	}
	resp.Body.Close()

	// Simulate a browser refresh: issue a fresh GET to the workbench root.
	refreshURL := fmt.Sprintf("%s/_proxy/%d/", cfg.BaseURL, vsCodePort)
	refreshResp, err := client.Get(refreshURL)
	if err != nil {
		t.Fatalf("GET refresh: %v", err)
	}
	defer refreshResp.Body.Close()

	body, _ := io.ReadAll(refreshResp.Body)
	bodyStr := string(body)

	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on refresh, got %d\nbody: %.500s", refreshResp.StatusCode, bodyStr)
	}
	if !strings.Contains(bodyStr, "codeServerVersion") {
		t.Fatalf("expected workbench markers after refresh, got:\n%.500s", bodyStr)
	}
	t.Log("✓ code-server workbench remains usable after refresh")
}

// TestVSCodeUnauthRedirectsToLogin verifies that accessing the workbench root
// without a valid session redirects back through login rather than loading
// the workbench (i.e., the proxy does not bypass code-server auth).
func TestVSCodeUnauthRedirectsToLogin(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	client := vsCodeClient(t, token)
	requireVSCode(t, client)

	// Deliberately use a fresh client with no code-server-session cookie.
	resp, err := client.Get(fmt.Sprintf("%s/_proxy/%d/", cfg.BaseURL, vsCodePort))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	finalURL := resp.Request.URL.String()
	t.Logf("final URL: %s (status %d)", finalURL, resp.StatusCode)

	// Without a session, code-server redirects to /login.
	onLoginPage := strings.Contains(finalURL, "/login") ||
		strings.Contains(bodyStr, `name="password"`)
	if !onLoginPage {
		t.Errorf("expected redirect to login page for unauthenticated request; "+
			"got final URL %s\nbody: %.300s", finalURL, bodyStr)
	}
	t.Log("✓ unauthenticated request correctly redirected to login")
}
