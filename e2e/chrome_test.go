package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type chromePage struct {
	conn *websocket.Conn
	proc *exec.Cmd

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan cdpResponse
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func chromeAvailable() bool {
	_, err := chromeBinary()
	return err == nil
}

func chromeBinary() (string, error) {
	for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no chrome binary found")
}

func launchChromePage(t *testing.T, baseURL string) (*chromePage, func()) {
	t.Helper()

	bin, err := chromeBinary()
	if err != nil {
		t.Skip(err)
	}

	tmpDir, err := os.MkdirTemp("", "webdesktopd-chrome-*")
	if err != nil {
		t.Fatalf("chrome tempdir: %v", err)
	}
	port := freePort(t)

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir=" + filepath.Join(tmpDir, "profile"),
		"--remote-debugging-port=" + port,
		"about:blank",
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start chrome: %v", err)
	}

	pageURL := waitForDebuggerPage(t, port)
	conn := dialCDP(t, pageURL)

	page := &chromePage{
		conn:    conn,
		proc:    cmd,
		pending: make(map[int64]chan cdpResponse),
	}
	go page.readLoop()

	page.call(t, "Page.enable", nil)
	page.call(t, "Runtime.enable", nil)
	page.call(t, "Network.enable", nil)
	page.call(t, "Page.navigate", map[string]any{"url": baseURL + "/"})

	cleanup := func() {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = conn.Close()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
		_ = os.RemoveAll(tmpDir)
	}

	return page, cleanup
}

type tunnelHandle struct {
	cmd     *exec.Cmd
	port    string
	baseURL string
	logs    *bytes.Buffer
}

func startTunnel(t *testing.T, port string) *tunnelHandle {
	t.Helper()

	logs := &bytes.Buffer{}
	root := repoRoot(t)
	cmd := exec.Command(
		filepath.Join(root, "tunnel"),
		"--host=127.0.0.1",
		"--port=32233",
		"--user=abb",
		"--pass="+cfg.Pass,
		"--local="+port,
		"--remote=18080",
	)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start tunnel: %v", err)
	}

	baseURL := "http://127.0.0.1:" + port
	waitForHTTP(t, baseURL)

	return &tunnelHandle{cmd: cmd, port: port, baseURL: baseURL, logs: logs}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve repo root")
	}
	return filepath.Dir(filepath.Dir(file))
}

func (th *tunnelHandle) stop() {
	if th == nil || th.cmd == nil || th.cmd.Process == nil {
		return
	}
	_ = th.cmd.Process.Kill()
	_, _ = th.cmd.Process.Wait()
}

func waitForHTTP(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			func() {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					lastErr = nil
					return
				}
				lastErr = fmt.Errorf("status %d", resp.StatusCode)
			}()
			if lastErr == nil {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("wait for %s: %v", baseURL, lastErr)
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)
}

func waitForDebuggerPage(t *testing.T, port string) string {
	t.Helper()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := "http://127.0.0.1:" + port + "/json/list"
	var lastErr error
	for i := 0; i < 60; i++ {
		resp, err := client.Get(url)
		if err == nil {
			func() {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
					return
				}
				var targets []struct {
					Type                 string `json:"type"`
					WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
					URL                  string `json:"url"`
					ID                   string `json:"id"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
					lastErr = err
					return
				}
				for _, target := range targets {
					if target.Type == "page" && target.WebSocketDebuggerURL != "" {
						lastErr = nil
						url = target.WebSocketDebuggerURL
						return
					}
				}
				lastErr = errors.New("no page target found")
			}()
			if lastErr == nil {
				return url
			}
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("wait for chrome debugger: %v", lastErr)
	return ""
}

func dialCDP(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial cdp %s: %v", wsURL, err)
	}
	return conn
}

func (p *chromePage) readLoop() {
	for {
		_, msg, err := p.conn.ReadMessage()
		if err != nil {
			return
		}

		var base struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(msg, &base); err == nil && base.ID != 0 {
			var resp cdpResponse
			if err := json.Unmarshal(msg, &resp); err != nil {
				continue
			}
			p.mu.Lock()
			ch := p.pending[resp.ID]
			delete(p.pending, resp.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- resp
			}
		}
	}
}

func (p *chromePage) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()

	p.mu.Lock()
	id := p.nextID + 1
	p.nextID = id
	ch := make(chan cdpResponse, 1)
	p.pending[id] = ch
	p.mu.Unlock()

	req := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		req["params"] = params
	}
	if err := p.conn.WriteJSON(req); err != nil {
		t.Fatalf("cdp send %s: %v", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			t.Fatalf("cdp %s: %s", method, resp.Error.Message)
		}
		return resp.Result
	case <-time.After(10 * time.Second):
		t.Fatalf("cdp %s timed out", method)
		return nil
	}
}

func (p *chromePage) evalString(t *testing.T, expr string) string {
	t.Helper()
	res := p.call(t, "Runtime.evaluate", map[string]any{
		"expression":        expr,
		"returnByValue":     true,
		"awaitPromise":      true,
		"replMode":          true,
		"throwOnSideEffect": false,
	})
	var parsed struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		t.Fatalf("decode runtime result: %v", err)
	}
	if parsed.Result.Value == nil {
		return ""
	}
	return fmt.Sprint(parsed.Result.Value)
}

func (p *chromePage) fillInput(t *testing.T, selector, value string) {
	t.Helper()
	script := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) throw new Error("missing input: %s");
		el.focus();
		el.value = %q;
		el.dispatchEvent(new Event('input', { bubbles: true, composed: true }));
		el.dispatchEvent(new Event('change', { bubbles: true, composed: true }));
	})()`, selector, selector, value)
	p.call(t, "Runtime.evaluate", map[string]any{
		"expression":        script,
		"returnByValue":     true,
		"awaitPromise":      true,
		"replMode":          true,
		"throwOnSideEffect": false,
	})
}

func (p *chromePage) submitForm(t *testing.T, selector string) {
	t.Helper()
	script := fmt.Sprintf(`(() => {
		const form = document.querySelector(%q);
		if (!form) throw new Error("missing form: %s");
		if (form.requestSubmit) {
			form.requestSubmit();
		} else {
			form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
		}
	})()`, selector, selector)
	p.call(t, "Runtime.evaluate", map[string]any{
		"expression":        script,
		"returnByValue":     true,
		"awaitPromise":      true,
		"replMode":          true,
		"throwOnSideEffect": false,
	})
}

func (p *chromePage) mustWaitForPathname(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := p.evalString(t, "window.location.pathname"); got == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pathname did not become %q within %s (got %q)", want, timeout, p.evalString(t, "window.location.pathname"))
}

func (p *chromePage) mustWaitForText(t *testing.T, selector string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if exists := p.evalString(t, fmt.Sprintf(`Boolean(document.querySelector(%q))`, selector)); exists == "true" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("selector %q not found within %s", selector, timeout)
}

func (p *chromePage) mustWaitForTextContains(t *testing.T, text string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body := p.evalString(t, "document.body.innerText")
		if strings.Contains(body, text) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("text %q not found within %s", text, timeout)
}

func (p *chromePage) mustNotContainText(t *testing.T, text string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body := p.evalString(t, "document.body.innerText")
		if !strings.Contains(body, text) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("text %q still present after %s", text, timeout)
}
