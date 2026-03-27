package e2e

import (
	"strings"
	"testing"
	"time"
)

// remoteTarget returns a TCP target reachable from the remote server.
// We proxy to webdesktopd itself on port 18080 — guaranteed to be running.
const remoteTarget = "127.0.0.1:18080"

func TestPortProxyTCPHTTP(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(3 * time.Second)

	// Open proxy to webdesktopd's own HTTP port.
	const proxyChanID uint16 = 50
	ch := c.subscribe(proxyChanID)
	defer c.unsubscribe(proxyChanID, ch)

	c.sendJSON(ftOpenProxy, 0, map[string]any{
		"channel": proxyChanID,
		"target":  remoteTarget,
	})
	time.Sleep(150 * time.Millisecond)

	// Send raw HTTP GET /health request.
	req := "GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	c.send(ftData, proxyChanID, []byte(req))

	// Collect data frames until CloseProxy (TCP close) or timeout.
	var gotData []byte
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
loop:
	for {
		select {
		case f := <-ch:
			switch f.Type {
			case ftData:
				gotData = append(gotData, f.Payload...)
			case ftCloseProxy:
				break loop
			}
		case <-deadline.C:
			break loop
		}
	}

	// /health returns {"status":"ok"}
	if !strings.Contains(string(gotData), "status") {
		t.Errorf("expected HTTP response with 'status', got: %q", string(gotData))
	}

	c.sendJSON(ftCloseProxy, 0, map[string]any{"channel": proxyChanID})
}

func TestPortProxyUnreachable(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(3 * time.Second)

	// Dial a port that nothing is listening on.
	const proxyChanID uint16 = 51
	ch := c.subscribe(0xFFFF) // watch all frames
	defer c.unsubscribe(0xFFFF, ch)

	c.sendJSON(ftOpenProxy, 0, map[string]any{
		"channel": proxyChanID,
		"target":  "127.0.0.1:19999", // unlikely to be in use
	})

	// Expect a CloseProxy frame (0x10) indicating connection failure.
	_, ok := c.collectUntil(ch, func(f wsFrame) bool {
		return f.Type == ftCloseProxy
	}, 3*time.Second)
	if !ok {
		t.Fatal("expected CloseProxy frame after unreachable target, none received")
	}
}

func TestPortProxySessionSync(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(3 * time.Second)

	// Open proxy to a real remote service.
	const proxyChanID uint16 = 52
	c.sendJSON(ftOpenProxy, 0, map[string]any{
		"channel": proxyChanID,
		"target":  remoteTarget,
	})
	time.Sleep(150 * time.Millisecond)
	c.Close()

	// Reconnect and verify proxy is listed in session sync.
	c2 := dial(t, token)
	defer c2.Close()
	result := c2.syncSession(3 * time.Second)

	found := false
	for _, p := range result.ProxyChannels {
		if ch, _ := p["chanID"].(float64); uint16(ch) == proxyChanID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("proxy chanID %d not found in session sync proxyChannels: %v", proxyChanID, result.ProxyChannels)
	}
}
