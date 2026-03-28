package pty

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"webdesktopd/internal/hub"
)

// TestResizeMsgRoundTrip verifies JSON marshal/unmarshal of ResizeMsg.
func TestResizeMsgRoundTrip(t *testing.T) {
	msg := ResizeMsg{Cols: 120, Rows: 40}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ResizeMsg
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cols != msg.Cols || got.Rows != msg.Rows {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, msg)
	}
}

// TestOpenMsgRoundTrip verifies JSON marshal/unmarshal of OpenMsg.
func TestOpenMsgRoundTrip(t *testing.T) {
	msg := OpenMsg{Channel: 3, Shell: "/bin/bash", CWD: "/home/alice"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OpenMsg
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Channel != msg.Channel || got.Shell != msg.Shell || got.CWD != msg.CWD {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, msg)
	}
}

// mockSender records frames sent to it.
type mockSender struct {
	frames chan hub.Frame
}

func newMockSender(buf int) *mockSender {
	return &mockSender{frames: make(chan hub.Frame, buf)}
}

func (m *mockSender) Send(f hub.Frame) error {
	select {
	case m.frames <- f:
	default:
	}
	return nil
}

// TestSpawnAndEcho is an integration test that spawns a shell as the current user,
// sends an echo command, and verifies the output appears in the ring buffer / sender.
// Skipped if not running as root or without required capabilities.
func TestSpawnAndEcho(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY test only runs on Linux")
	}

	// Check if we can spawn processes as current user.
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}

	// Only run the full PTY spawn test if we are root (UID 0) or
	// if WEBDESKTOPD_TEST_PTY is set (indicates environment has caps).
	isRoot := os.Getenv("WEBDESKTOPD_TEST_PTY") != "" || currentUser.Uid == "0"
	if !isRoot {
		t.Skip("skipping PTY spawn test: requires root or WEBDESKTOPD_TEST_PTY env var")
	}

	sender := newMockSender(32)

	session, err := New(1, currentUser.Username, "/bin/sh", currentUser.HomeDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer session.Close()

	session.Attach(sender)

	// Send an echo command.
	echoCmd := []byte("echo webdesktopd-test-marker\n")
	if _, err := session.ptmx.Write(echoCmd); err != nil {
		t.Fatalf("write to ptmx: %v", err)
	}

	// Collect output for up to 3 seconds.
	var output []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case f := <-sender.frames:
			output = append(output, f.Payload...)
			if containsBytes(output, []byte("webdesktopd-test-marker")) {
				return // success
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Errorf("did not receive expected output; got: %q", output)
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestRingBufferIntegration verifies that output goes into the ring buffer.
func TestRingBufferIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY test only runs on Linux")
	}
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}
	isRoot := os.Getenv("WEBDESKTOPD_TEST_PTY") != "" || currentUser.Uid == "0"
	if !isRoot {
		t.Skip("skipping PTY ring buffer test: requires root or WEBDESKTOPD_TEST_PTY env var")
	}

	session, err := New(2, currentUser.Username, "/bin/sh", currentUser.HomeDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer session.Close()

	// Don't attach sender – output should still go to ring buffer.
	session.ptmx.Write([]byte("echo ringtest\n")) //nolint:errcheck

	// Wait for ring buffer to capture output.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if containsBytes(session.ring.Bytes(), []byte("ringtest")) {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("ring buffer did not contain expected output; got: %q", session.ring.Bytes())
}

// TestInteractiveShellSourcesBashrc verifies that a new PTY starts an
// interactive shell, so bash reads .bashrc on startup.
func TestInteractiveShellSourcesBashrc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY test only runs on Linux")
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}

	isRoot := os.Getenv("WEBDESKTOPD_TEST_PTY") != "" || currentUser.Uid == "0"
	if !isRoot {
		t.Skip("skipping PTY bashrc test: requires root or WEBDESKTOPD_TEST_PTY env var")
	}

	tmpHome := t.TempDir()
	marker := "BASHRC_LOADED_TEST"
	if err := os.WriteFile(filepath.Join(tmpHome, ".bashrc"), []byte("echo "+marker+"\n"), 0o644); err != nil {
		t.Fatalf("write .bashrc: %v", err)
	}

	wrapper := filepath.Join(tmpHome, "bash-wrapper.sh")
	script := "#!/bin/sh\nexport HOME=\"" + tmpHome + "\"\nexec /bin/bash \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	sender := newMockSender(32)
	session, err := New(3, currentUser.Username, wrapper, currentUser.HomeDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer session.Close()

	session.Attach(sender)

	deadline := time.Now().Add(5 * time.Second)
	var output []byte
	for time.Now().Before(deadline) {
		select {
		case f := <-sender.frames:
			output = append(output, f.Payload...)
			if containsBytes(output, []byte(marker)) {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
	}

	t.Fatalf(".bashrc marker not observed; got: %q", output)
}
