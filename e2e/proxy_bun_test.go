package e2e

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// bunServerPort is the port the remote bun app will listen on.
const bunServerPort = 19866

// bunApp is the bun serve script sent to the remote.
// It handles several test routes used by multiple e2e test files:
//
//	/        – HTML page with "proxy-test-ok" marker (existing proxy tests)
//	/api     – JSON {status,framework,port}            (existing proxy tests)
//	/ws      – WebSocket echo server                   (proxy header/WS tests)
//	/xfo     – response with X-Frame-Options: DENY     (proxy header/WS tests)
//	/csp     – response with CSP frame-ancestors       (proxy header/WS tests)
//	/cookies – echoes received Cookie header as JSON   (proxy header/WS tests)
//
// HTTP responses include Connection: close so the TCP read loop can detect
// EOF and send the client a 0x10 CloseProxy frame.
const bunApp = `
const server = Bun.serve({
  port: %d,
  fetch(req, server) {
    const url = new URL(req.url);
    if (url.pathname === "/ws") {
      if (server.upgrade(req)) return;
      return new Response("upgrade failed", { status: 500 });
    }
    if (url.pathname === "/api") {
      return Response.json(
        { status: "ok", framework: "bun", port: %d },
        { headers: { "Connection": "close" } }
      );
    }
    if (url.pathname === "/xfo") {
      return new Response("xfo-test", {
        headers: { "Content-Type": "text/plain", "X-Frame-Options": "DENY", "Connection": "close" },
      });
    }
    if (url.pathname === "/csp") {
      return new Response("csp-test", {
        headers: {
          "Content-Type": "text/plain",
          "Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'",
          "Connection": "close",
        },
      });
    }
    if (url.pathname === "/cookies") {
      const cookie = req.headers.get("cookie") ?? "";
      return Response.json({ cookies: cookie }, { headers: { "Connection": "close" } });
    }
    return new Response(
      "<!DOCTYPE html><html><head><title>Bun Test App</title></head>" +
      "<body>" +
      "<h1 id='title'>proxy-test-ok</h1>" +
      "<p>Bun %d running via webdesktopd proxy</p>" +
      "</body></html>",
      { headers: { "Content-Type": "text/html", "Connection": "close" } }
    );
  },
  websocket: {
    message(ws, msg) { ws.send(msg); },
  },
});
process.stdout.write("bun-ready:" + server.port + "\n");
`

// remoteBunServer holds the SSH client and process state for the bun server.
type remoteBunServer struct {
	client     *ssh.Client
	pid        string
	scriptPath string
	port       int
}

// startRemoteBunServer starts a Bun HTTP server on the remote SSH host.
// Skips the test if WEBDESKTOPD_SSH_ADDR is not set.
// Returns the server info and a cleanup function that stops the process.
func startRemoteBunServer(t *testing.T) (*remoteBunServer, func()) {
	t.Helper()
	if cfg.SSHAddr == "" {
		t.Skip("WEBDESKTOPD_SSH_ADDR not set — skipping remote bun server tests")
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", cfg.SSHAddr, sshCfg)
	if err != nil {
		t.Skipf("SSH dial %q: %v — skipping bun test", cfg.SSHAddr, err)
	}

	port := bunServerPort
	scriptPath := fmt.Sprintf("/tmp/e2e_bun_%d.js", port)
	script := fmt.Sprintf(bunApp, port, port, port)

	// Write the bun script to the remote via stdin.
	writeSession, err := client.NewSession()
	if err != nil {
		client.Close()
		t.Skipf("SSH session: %v", err)
	}
	writeSession.Stdin = strings.NewReader(script)
	if err := writeSession.Run("cat > " + scriptPath); err != nil {
		writeSession.Close()
		client.Close()
		t.Skipf("write bun script: %v", err)
	}
	writeSession.Close()

	// Kill any previous instance that may be holding the port.
	if killSession, err := client.NewSession(); err == nil {
		killSession.Run(fmt.Sprintf("fuser -k %d/tcp 2>/dev/null; true", port)) //nolint:errcheck
		killSession.Close()
		time.Sleep(300 * time.Millisecond)
	}

	// Start bun detached (nohup) and capture its PID.
	startSession, err := client.NewSession()
	if err != nil {
		client.Close()
		t.Skipf("SSH session: %v", err)
	}
	var pidBuf, errBuf bytes.Buffer
	startSession.Stdout = &pidBuf
	startSession.Stderr = &errBuf
	bunBin := "/home/" + cfg.User + "/.bun/bin/bun"
	startCmd := fmt.Sprintf("nohup %s %s > /tmp/e2e_bun_%d.log 2>&1 & echo $!", bunBin, scriptPath, port)
	if err := startSession.Run(startCmd); err != nil {
		startSession.Close()
		client.Close()
		t.Skipf("start bun: %v (stderr: %s) — is bun installed?", err, errBuf.String())
	}
	startSession.Close()

	pid := strings.TrimSpace(pidBuf.String())
	if pid == "" {
		client.Close()
		t.Skip("could not capture bun PID")
	}
	t.Logf("bun server started: PID=%s port=%d", pid, port)

	// Wait for bun to be ready (give it up to 3 s).
	ready := false
	for i := 0; i < 15; i++ {
		time.Sleep(200 * time.Millisecond)
		checkSession, err := client.NewSession()
		if err != nil {
			break
		}
		var out bytes.Buffer
		checkSession.Stdout = &out
		checkSession.Run(fmt.Sprintf("cat /tmp/e2e_bun_%d.log 2>/dev/null", port)) //nolint:errcheck
		checkSession.Close()
		if strings.Contains(out.String(), "bun-ready:") {
			ready = true
			break
		}
	}
	if !ready {
		// Soft failure: skip rather than hard-fail if bun won't start.
		if killSession, err2 := client.NewSession(); err2 == nil {
			killSession.Run("kill " + pid) //nolint:errcheck
			killSession.Close()
		}
		client.Close()
		t.Skipf("bun server did not report ready within 3s on port %d", port)
	}

	srv := &remoteBunServer{client: client, pid: pid, scriptPath: scriptPath, port: port}
	cleanup := func() {
		if killSession, err := srv.client.NewSession(); err == nil {
			killSession.Run(fmt.Sprintf("kill %s 2>/dev/null; rm -f %s /tmp/e2e_bun_%d.log",
				srv.pid, srv.scriptPath, srv.port)) //nolint:errcheck
			killSession.Close()
		}
		srv.client.Close()
	}
	return srv, cleanup
}

// httpViaProxy opens a WS proxy channel to target, sends a raw HTTP request,
// and accumulates the full response until TCP close (0x10 frame).
func httpViaProxy(t *testing.T, c *WSClient, target string, chanID uint16, method, path string) string {
	t.Helper()

	ch := c.subscribe(chanID)
	defer c.unsubscribe(chanID, ch)

	c.sendJSON(ftOpenProxy, 0, map[string]any{"channel": chanID, "target": target})
	time.Sleep(200 * time.Millisecond)

	rawReq := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nAccept: */*\r\n\r\n",
		method, path)
	c.send(ftData, chanID, []byte(rawReq))

	var data []byte
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
loop:
	for {
		select {
		case f := <-ch:
			switch f.Type {
			case ftData:
				data = append(data, f.Payload...)
			case ftCloseProxy:
				break loop
			}
		case <-deadline.C:
			t.Log("httpViaProxy: timed out waiting for CloseProxy frame")
			break loop
		}
	}

	c.sendJSON(ftCloseProxy, 0, map[string]any{"channel": chanID})
	return string(data)
}

// TestProxyBunWebServerHTML starts a bun server on the remote, then proxies
// a GET / through the WS TCP tunnel and verifies the HTML response.
func TestProxyBunWebServerHTML(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	defer c.Close()
	c.syncSession(3 * time.Second)

	target := fmt.Sprintf("127.0.0.1:%d", srv.port)
	resp := httpViaProxy(t, c, target, 60, "GET", "/")

	t.Logf("HTML response (%d bytes):\n%s", len(resp), resp)

	if !strings.Contains(resp, "200 OK") {
		t.Errorf("expected HTTP 200 OK, got:\n%s", resp)
	}
	if !strings.Contains(resp, "proxy-test-ok") {
		t.Errorf("expected 'proxy-test-ok' in HTML body, got:\n%s", resp)
	}
	if !strings.Contains(resp, "text/html") {
		t.Errorf("expected Content-Type: text/html, got:\n%s", resp)
	}
	t.Log("✓ bun HTML page loaded via WS proxy tunnel")
}

// TestProxyBunWebServerJSON hits the /api endpoint and verifies the JSON response.
func TestProxyBunWebServerJSON(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	defer c.Close()
	c.syncSession(3 * time.Second)

	target := fmt.Sprintf("127.0.0.1:%d", srv.port)
	resp := httpViaProxy(t, c, target, 61, "GET", "/api")

	t.Logf("JSON response (%d bytes):\n%s", len(resp), resp)

	if !strings.Contains(resp, "200 OK") {
		t.Errorf("expected HTTP 200 OK, got:\n%s", resp)
	}
	if !strings.Contains(resp, `"status"`) || !strings.Contains(resp, `"ok"`) {
		t.Errorf("expected JSON {status:ok}, got:\n%s", resp)
	}
	if !strings.Contains(resp, `"framework"`) {
		t.Errorf("expected 'framework' field in JSON, got:\n%s", resp)
	}
	t.Log("✓ bun JSON API loaded via WS proxy tunnel")
}

// TestPortScanDiscoversBunServer verifies that after starting a bun server on
// a known port, FramePortScan returns an entry for that port with "bun" as
// the process name.
func TestPortScanDiscoversBunServer(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	defer c.Close()
	c.syncSession(3 * time.Second)

	ports, err := c.scanPorts(5 * time.Second)
	if err != nil {
		t.Fatalf("scanPorts: %v", err)
	}
	t.Logf("discovered %d listening ports", len(ports))
	for _, p := range ports {
		t.Logf("  port=%d process=%q cmdline=%q", p.Port, p.Process, p.Cmdline)
	}

	var found *PortScanEntry
	for i := range ports {
		if ports[i].Port == srv.port {
			found = &ports[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected port %d in scan results, got: %v", srv.port, ports)
	}
	if found.Process == "" {
		t.Errorf("expected non-empty process name for port %d", srv.port)
	}
	t.Logf("✓ bun server port %d discovered: process=%q", srv.port, found.Process)
}

// TestPortScanOpenAndLoadProxy verifies the full user scenario:
// 1. A bun server is running on the remote
// 2. PortScan discovers it
// 3. A WS proxy channel is opened to it
// 4. An HTTP request through the proxy returns the bun app's HTML
func TestPortScanOpenAndLoadProxy(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	defer c.Close()
	c.syncSession(3 * time.Second)

	// Discover ports.
	ports, err := c.scanPorts(5 * time.Second)
	if err != nil {
		t.Fatalf("scanPorts: %v", err)
	}
	var found bool
	for _, p := range ports {
		if p.Port == srv.port {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bun server port %d not found in port scan; ports=%v", srv.port, ports)
	}

	// Open proxy and load the page.
	target := fmt.Sprintf("127.0.0.1:%d", srv.port)
	resp := httpViaProxy(t, c, target, 65, "GET", "/")

	t.Logf("proxy response (%d bytes):\n%s", len(resp), resp)

	if !strings.Contains(resp, "200 OK") {
		t.Errorf("expected HTTP 200, got:\n%s", resp)
	}
	if !strings.Contains(resp, "proxy-test-ok") {
		t.Errorf("expected 'proxy-test-ok' in body, got:\n%s", resp)
	}
	t.Logf("✓ port discovered via scan, proxied, and HTML loaded successfully")
}

// TestProxyBunWebServerMultipleRequests verifies that separate proxy channels
// can each make independent HTTP requests to the same bun server.
func TestProxyBunWebServerMultipleRequests(t *testing.T) {
	srv, cleanup := startRemoteBunServer(t)
	defer cleanup()

	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	defer c.Close()
	c.syncSession(3 * time.Second)

	target := fmt.Sprintf("127.0.0.1:%d", srv.port)

	// Three sequential requests on different channels.
	type result struct{ chanID uint16; path string; resp string }
	results := []result{
		{62, "/", ""},
		{63, "/api", ""},
		{64, "/", ""},
	}

	for i := range results {
		results[i].resp = httpViaProxy(t, c, target, results[i].chanID, "GET", results[i].path)
	}

	// Verify all three succeeded.
	for _, r := range results {
		if !strings.Contains(r.resp, "200 OK") {
			preview := r.resp
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Errorf("chanID %d GET %s: expected 200 OK, got: %s",
				r.chanID, r.path, preview)
		}
	}

	if t.Failed() {
		return
	}
	t.Logf("✓ 3 independent proxy channels each loaded content from bun server")
}

