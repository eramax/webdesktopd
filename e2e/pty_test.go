package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPTYOpenAndEcho opens a PTY, sends an echo command, verifies the output.
func TestPTYOpenAndEcho(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	chanID := uint16(10)
	c.openPTY(chanID, "/bin/bash", "")
	time.Sleep(800 * time.Millisecond) // let shell start

	marker := fmt.Sprintf("E2E_ECHO_%d", time.Now().UnixNano())
	c.sendInput(chanID, "echo "+marker+"\n")

	out, ok := c.waitForOutput(chanID, marker, 5*time.Second)
	if !ok {
		t.Fatalf("echo marker %q not found in output within 5s.\nOutput: %q", marker, out)
	}
	t.Logf("✓ PTY echo OK: marker found")

	// Close the PTY cleanly.
	c.sendJSON(ftClosePTY, 0, map[string]any{"channel": chanID})
}

// TestPTYRunsAsCorrectUser verifies that `id` returns the expected username.
func TestPTYRunsAsCorrectUser(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	chanID := uint16(11)
	c.openPTY(chanID, "/bin/bash", "")
	time.Sleep(800 * time.Millisecond)

	c.sendInput(chanID, "id\n")
	out, ok := c.waitForOutput(chanID, "uid=", 5*time.Second)
	if !ok {
		t.Fatalf("'id' output not received. Got: %q", out)
	}
	if !strings.Contains(out, cfg.User) {
		t.Fatalf("expected username %q in id output, got: %q", cfg.User, out)
	}
	t.Logf("✓ id: %s", extractLine(out, "uid="))

	c.sendJSON(ftClosePTY, 0, map[string]any{"channel": chanID})
}

// TestPTYMultipleTabs verifies that two PTY sessions can run independently.
func TestPTYMultipleTabs(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	// Open two PTY channels.
	c.openPTY(20, "/bin/bash", "")
	c.openPTY(21, "/bin/bash", "")
	time.Sleep(time.Second)

	marker1 := "E2E_TAB1_MARKER"
	marker2 := "E2E_TAB2_MARKER"

	// Subscribe to both channels BEFORE sending input so no output is missed.
	type result struct {
		out string
		ok  bool
	}
	res1 := make(chan result, 1)
	res2 := make(chan result, 1)

	go func() {
		out, ok := c.waitForOutput(20, marker1, 8*time.Second)
		res1 <- result{out, ok}
	}()
	go func() {
		out, ok := c.waitForOutput(21, marker2, 8*time.Second)
		res2 <- result{out, ok}
	}()

	// Small delay so goroutines are subscribed before we send.
	time.Sleep(50 * time.Millisecond)
	c.sendInput(20, "echo "+marker1+"\n")
	c.sendInput(21, "echo "+marker2+"\n")

	r1 := <-res1
	r2 := <-res2

	if !r1.ok {
		t.Errorf("tab 1: marker %q not found in %q", marker1, r1.out)
	}
	if !r2.ok {
		t.Errorf("tab 2: marker %q not found in %q", marker2, r2.out)
	}
	if r1.ok && r2.ok {
		t.Log("✓ two independent PTY tabs work correctly")
	}

	c.sendJSON(ftClosePTY, 0, map[string]any{"channel": 20})
	c.sendJSON(ftClosePTY, 0, map[string]any{"channel": 21})
}

// TestPTYRingBufferReconnect verifies that output produced while disconnected
// is replayed from the ring buffer on the next connection.
func TestPTYRingBufferReconnect(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	chanID := uint16(30)

	// Close any stale PTY on this channel from a previous failed run.
	pre := dial(t, token)
	pre.sendJSON(ftClosePTY, 0, map[string]any{"channel": chanID})
	pre.Close()
	time.Sleep(100 * time.Millisecond)

	// First connection: open PTY, run a command, then disconnect.
	c1 := dial(t, token)
	c1.syncSession(2 * time.Second)
	c1.openPTY(chanID, "/bin/bash", "")
	time.Sleep(800 * time.Millisecond)

	marker := fmt.Sprintf("RINGBUF_%d", time.Now().UnixNano())
	c1.sendInput(chanID, "echo "+marker+"\n")

	// Wait for the output to be produced and stored in the ring buffer.
	_, ok := c1.waitForOutput(chanID, marker, 5*time.Second)
	if !ok {
		t.Fatalf("marker not produced before disconnect")
	}
	t.Log("✓ marker produced on first connection")

	// Disconnect by closing c1 (PTY keeps running, output goes to ring buffer).
	c1.Close()
	time.Sleep(300 * time.Millisecond)

	// Second connection: should receive the ring buffer replay containing the marker.
	c2 := dial(t, token)

	// Subscribe before sending openPTY so no replay frames are missed.
	ch := c2.subscribe(chanID)
	defer c2.unsubscribe(chanID, ch)

	// Sync first to learn which channels are open, then re-open the channel.
	// The server replays the ring buffer on OpenPTY for an already-running PTY.
	c2.syncSession(500 * time.Millisecond)
	c2.openPTY(chanID, "/bin/bash", "")

	// Collect everything for up to 3 seconds — ring buffer replay should arrive.
	raw, found := c2.collectUntil(ch, func(f wsFrame) bool {
		return strings.Contains(printableE2E(f.Payload), marker)
	}, 3*time.Second)

	if !found {
		t.Fatalf("ring buffer replay did not contain marker %q\nGot: %q", marker, printableE2E(raw))
	}
	t.Log("✓ ring buffer replay received on reconnect")

	c2.sendJSON(ftClosePTY, 0, map[string]any{"channel": chanID})
}

// TestPTYResize verifies that FramePTYResize (0x02) is accepted without error.
func TestPTYResize(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	chanID := uint16(40)
	c.openPTY(chanID, "/bin/bash", "")
	time.Sleep(800 * time.Millisecond)

	// Send resize.
	resize, _ := json.Marshal(map[string]int{"cols": 220, "rows": 50})
	c.send(ftPTYResize, chanID, resize)

	// Verify terminal still works after resize.
	marker := "AFTER_RESIZE_OK"
	c.sendInput(chanID, "echo "+marker+"\n")
	out, ok := c.waitForOutput(chanID, marker, 5*time.Second)
	if !ok {
		t.Fatalf("no output after resize: %q", out)
	}
	t.Log("✓ PTY resize accepted, terminal still functional")

	c.sendJSON(ftClosePTY, 0, map[string]any{"channel": chanID})
}

// TestPTYSessionSync verifies that the session-sync frame lists open PTYs.
func TestPTYSessionSync(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)

	// First connection: open a PTY.
	c1 := dial(t, token)
	c1.syncSession(2 * time.Second)

	chanID := uint16(50)
	c1.openPTY(chanID, "/bin/bash", "")
	time.Sleep(500 * time.Millisecond)
	c1.Close()
	time.Sleep(300 * time.Millisecond)

	// Second connection: session sync should list chanID 50.
	c2 := dial(t, token)
	defer c2.Close()
	channels := c2.syncSession(2 * time.Second)

	found := false
	for _, ch := range channels {
		if id, ok := ch["chanID"].(float64); ok && uint16(id) == chanID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("chanID %d not found in session-sync channels: %v", chanID, channels)
	}
	t.Logf("✓ session-sync lists chanID %d", chanID)

	c2.sendJSON(ftClosePTY, 0, map[string]any{"channel": chanID})
}

// extractLine returns the first line in s that contains substr.
func extractLine(s, substr string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
