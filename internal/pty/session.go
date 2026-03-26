package pty

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"

	creackpty "github.com/creack/pty"
	"webdesktopd/internal/hub"
	"webdesktopd/internal/ringbuf"
)

const DefaultRingCap = 1 << 20 // 1MB

// Sender is the interface for sending frames to a WebSocket client.
type Sender interface {
	Send(f hub.Frame) error
}

// ResizeMsg is the JSON payload for FramePTYResize (0x02).
type ResizeMsg struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// OpenMsg is the JSON payload for FrameOpenPTY (0x0A).
type OpenMsg struct {
	Channel uint16 `json:"channel"`
	Shell   string `json:"shell"`
	CWD     string `json:"cwd"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
}

// Session represents a single PTY terminal session.
type Session struct {
	ChanID   uint16
	username string
	cmd      *exec.Cmd
	ptmx     *os.File
	ring     *ringbuf.RingBuffer

	senderMu sync.Mutex
	sender   Sender // nil when WS is disconnected

	closeOnce sync.Once
	done      chan struct{}
}

// lookupLoginShell returns the login shell for a user by reading /etc/passwd.
// Falls back to /bin/bash on any error.
func lookupLoginShell(username string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return "/bin/bash"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		// passwd format: username:password:uid:gid:gecos:home:shell
		if len(fields) >= 7 && fields[0] == username {
			shell := fields[6]
			if shell != "" {
				return shell
			}
		}
	}
	return "/bin/bash"
}

// New spawns a shell as the given Unix user in a PTY.
// shell defaults to the user's login shell from /etc/passwd (or /bin/bash).
// cwd defaults to the user's home directory.
func New(chanID uint16, username, shell, cwd string) (*Session, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("lookup user %q: %w", username, err)
	}

	uid64, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse uid %q: %w", u.Uid, err)
	}
	gid64, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}
	uid := uint32(uid64)
	gid := uint32(gid64)

	if shell == "" {
		shell = lookupLoginShell(username)
	}
	if cwd == "" {
		cwd = u.HomeDir
	}

	env := []string{
		"HOME=" + u.HomeDir,
		"USER=" + username,
		"LOGNAME=" + username,
		"SHELL=" + shell,
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}

	cmd := exec.Command(shell, "--login")
	cmd.Env = env
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Only set Credential when we actually need to change UID/GID.
	// setgroups() (called by the Go runtime when Credential is set) requires
	// CAP_SETGID even when keeping the same GID, so skip it when spawning
	// as the current user.
	if uint32(os.Getuid()) != uid || uint32(os.Getgid()) != gid {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid: uid,
			Gid: gid,
		}
	}

	ptmx, err := creackpty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	s := &Session{
		ChanID:   chanID,
		username: username,
		cmd:      cmd,
		ptmx:     ptmx,
		ring:     ringbuf.New(DefaultRingCap),
		done:     make(chan struct{}),
	}

	go s.reader()
	return s, nil
}

// Attach sets the sender. It first replays the ring buffer via sender, then starts live forwarding.
// Safe to call concurrently; replaces any previous sender.
func (s *Session) Attach(sender Sender) {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	// Replay ring buffer contents so the client sees previous output.
	data := s.ring.Bytes()
	if len(data) > 0 {
		if err := sender.Send(hub.Frame{Type: hub.FrameData, ChanID: s.ChanID, Payload: data}); err != nil {
			slog.Warn("pty: replay send error", "chanID", s.ChanID, "err", err)
		}
	}
	s.sender = sender
}

// Resize sets the PTY window size. Safe to call at any time.
func (s *Session) Resize(cols, rows uint16) {
	creackpty.Setsize(s.ptmx, &creackpty.Winsize{ //nolint:errcheck
		Cols: cols,
		Rows: rows,
	})
}

// Detach removes the sender, so output goes only to the ring buffer.
func (s *Session) Detach() {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	s.sender = nil
}

// HandleFrame implements hub.ChannelHandler.
// Handles FrameData (write to PTY) and FramePTYResize.
func (s *Session) HandleFrame(ctx context.Context, f hub.Frame) error {
	switch f.Type {
	case hub.FrameData:
		if _, err := s.ptmx.Write(f.Payload); err != nil {
			return fmt.Errorf("write to ptmx: %w", err)
		}
	case hub.FramePTYResize:
		var msg ResizeMsg
		if err := json.Unmarshal(f.Payload, &msg); err != nil {
			return fmt.Errorf("parse resize message: %w", err)
		}
		if msg.Cols == 0 {
			msg.Cols = 80
		}
		if msg.Rows == 0 {
			msg.Rows = 24
		}
		if err := creackpty.Setsize(s.ptmx, &creackpty.Winsize{
			Rows: msg.Rows,
			Cols: msg.Cols,
		}); err != nil {
			return fmt.Errorf("setsize: %w", err)
		}
	default:
		slog.Warn("pty: unhandled frame type", "type", f.Type, "chanID", s.ChanID)
	}
	return nil
}

// Close kills the shell process and all its children, then closes the PTY.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.cmd.Process != nil {
			// Kill the entire process group (negative PID targets the group).
			// This ensures children like btop/vim/etc. are also terminated.
			syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
		}
		s.ptmx.Close()
		// Wait for the process to avoid zombies.
		s.cmd.Wait() //nolint:errcheck
	})
}

// reader is the goroutine that reads from ptmx, writes to the ring buffer,
// and sends via sender (under senderMu) if non-nil.
func (s *Session) reader() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		n, err := s.ptmx.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.senderMu.Lock()
			s.ring.Write(data)
			if s.sender != nil {
				if sendErr := s.sender.Send(hub.Frame{
					Type:    hub.FrameData,
					ChanID:  s.ChanID,
					Payload: data,
				}); sendErr != nil {
					slog.Warn("pty: send frame error", "chanID", s.ChanID, "err", sendErr)
				}
			}
			s.senderMu.Unlock()
		}
		if err != nil {
			// PTY closed or EOF is normal on shell exit.
			return
		}
	}
}
