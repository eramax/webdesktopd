package e2e

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---- Frame constants -------------------------------------------------------

const (
	ftData        byte = 0x01
	ftPTYResize   byte = 0x02
	ftStats       byte = 0x03
	ftFileList    byte = 0x04
	ftFileResp    byte = 0x05
	ftFileUpload  byte = 0x06
	ftProgress    byte = 0x07
	ftFileDownReq byte = 0x08
	ftFileDown    byte = 0x09
	ftOpenPTY     byte = 0x0A
	ftClosePTY    byte = 0x0B
	ftSessionSync byte = 0x0C
	ftPing        byte = 0x0D
	ftPong        byte = 0x0E
	ftOpenProxy   byte = 0x0F
	ftCloseProxy  byte = 0x10
	ftFileOp      byte = 0x11
	ftDeskPush    byte = 0x12
	ftDeskSave    byte = 0x13
)

// ---- Wire frame ------------------------------------------------------------

type wsFrame struct {
	Type    byte
	ChanID  uint16
	Payload []byte
}

func encodeFrame(typ byte, chanID uint16, payload []byte) []byte {
	buf := make([]byte, 7+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint16(buf[1:3], chanID)
	binary.BigEndian.PutUint32(buf[3:7], uint32(len(payload)))
	copy(buf[7:], payload)
	return buf
}

func decodeFrame(msg []byte) (wsFrame, error) {
	if len(msg) < 7 {
		return wsFrame{}, fmt.Errorf("frame too short (%d bytes)", len(msg))
	}
	f := wsFrame{
		Type:   msg[0],
		ChanID: binary.BigEndian.Uint16(msg[1:3]),
	}
	length := binary.BigEndian.Uint32(msg[3:7])
	if int(length) > len(msg)-7 {
		return wsFrame{}, fmt.Errorf("frame payload truncated")
	}
	f.Payload = make([]byte, length)
	copy(f.Payload, msg[7:7+length])
	return f, nil
}

// ---- Auth helper -----------------------------------------------------------

// authResponse is the JSON response from POST /auth.
type authResponse struct {
	Token string `json:"token"`
	Error string `json:"error"`
}

// mustAuth authenticates and returns a JWT. Fails the test on error.
func mustAuth(t *testing.T, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(cfg.BaseURL+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	defer resp.Body.Close()
	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		t.Fatalf("decode /auth response: %v", err)
	}
	if ar.Token == "" {
		t.Fatalf("mustAuth: no token returned (HTTP %d, error=%q)", resp.StatusCode, ar.Error)
	}
	return ar.Token
}

// ---- WSClient --------------------------------------------------------------

// WSClient wraps a WebSocket connection with a frame-dispatch goroutine.
// Frames are dispatched to per-channel subscribers. Multiple goroutines may
// read from different channels concurrently.
type WSClient struct {
	t    *testing.T
	conn *websocket.Conn

	mu   sync.Mutex
	subs map[uint16][]chan wsFrame // chanID → list of subscriber channels

	done chan struct{}
}

// dial opens a WebSocket to /ws?token=JWT and starts the reader goroutine.
func dial(t *testing.T, token string) *WSClient {
	t.Helper()
	wsURL := cfg.WSURL + "/ws?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial %s: %v", wsURL, err)
	}
	c := &WSClient{
		t:    t,
		conn: conn,
		subs: make(map[uint16][]chan wsFrame),
		done: make(chan struct{}),
	}
	go c.reader()
	t.Cleanup(c.Close)
	return c
}

// reader dispatches incoming frames to per-channel subscriber channels.
func (c *WSClient) reader() {
	defer close(c.done)
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		f, err := decodeFrame(msg)
		if err != nil {
			continue
		}
		c.mu.Lock()
		for _, ch := range c.subs[f.ChanID] {
			select {
			case ch <- f:
			default: // drop if subscriber is full
			}
		}
		// Also dispatch every frame to the wildcard channel (chanID 0xFFFF).
		for _, ch := range c.subs[0xFFFF] {
			select {
			case ch <- f:
			default:
			}
		}
		c.mu.Unlock()
	}
}

// subscribe returns a channel that receives all frames for chanID.
// Use chanID 0xFFFF to receive all frames regardless of channel.
// The returned channel is closed when the connection closes.
// Call unsubscribe when done to avoid leaking.
func (c *WSClient) subscribe(chanID uint16) chan wsFrame {
	ch := make(chan wsFrame, 256)
	c.mu.Lock()
	c.subs[chanID] = append(c.subs[chanID], ch)
	c.mu.Unlock()
	return ch
}

func (c *WSClient) unsubscribe(chanID uint16, ch chan wsFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.subs[chanID]
	for i, s := range list {
		if s == ch {
			c.subs[chanID] = append(list[:i], list[i+1:]...)
			break
		}
	}
}

// send writes a binary frame to the WebSocket.
func (c *WSClient) send(typ byte, chanID uint16, payload []byte) {
	c.t.Helper()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, encodeFrame(typ, chanID, payload)); err != nil {
		c.t.Errorf("WS send: %v", err)
	}
}

func (c *WSClient) sendJSON(typ byte, chanID uint16, v any) {
	payload, _ := json.Marshal(v)
	c.send(typ, chanID, payload)
}

// collectUntil reads from ch until predicate(frame) returns true or timeout.
// Returns all payload bytes accumulated and whether the predicate matched.
func (c *WSClient) collectUntil(ch <-chan wsFrame, pred func(wsFrame) bool, timeout time.Duration) ([]byte, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var buf []byte
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return buf, false
			}
			buf = append(buf, f.Payload...)
			if pred(f) {
				return buf, true
			}
		case <-deadline.C:
			return buf, false
		}
	}
}

// drain reads all frames from ch for up to timeout and returns the payload bytes.
func (c *WSClient) drain(ch <-chan wsFrame, timeout time.Duration) []byte {
	out, _ := c.collectUntil(ch, func(wsFrame) bool { return false }, timeout)
	return out
}

// Close shuts down the WebSocket connection.
func (c *WSClient) Close() {
	c.conn.WriteMessage(websocket.CloseMessage, //nolint:errcheck
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.conn.Close()
}

// ---- PTY helpers -----------------------------------------------------------

// openPTY sends FrameOpenPTY on the control channel (chanID 0).
func (c *WSClient) openPTY(chanID uint16, shell, cwd string) {
	c.sendJSON(ftOpenPTY, 0, map[string]any{
		"channel": chanID,
		"shell":   shell,
		"cwd":     cwd,
	})
}

// sendInput writes raw bytes to a PTY channel.
func (c *WSClient) sendInput(chanID uint16, input string) {
	c.send(ftData, chanID, []byte(input))
}

// waitForOutput opens a subscriber for chanID, waits for marker to appear in
// the accumulated output, and returns the full output string.
func (c *WSClient) waitForOutput(chanID uint16, marker string, timeout time.Duration) (string, bool) {
	ch := c.subscribe(chanID)
	defer c.unsubscribe(chanID, ch)
	raw, ok := c.collectUntil(ch, func(f wsFrame) bool {
		return f.ChanID == chanID && strings.Contains(printableE2E(f.Payload), marker)
	}, timeout)
	return printableE2E(raw), ok
}

// ---- File helpers ----------------------------------------------------------

// FileInfo mirrors the server's JSON response for FrameFileListResp.
type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	Mode    string `json:"mode"`
	ModTime string `json:"modTime"`
}

// listDir sends FrameFileList and waits for FrameFileListResp.
func (c *WSClient) listDir(path string, timeout time.Duration) ([]FileInfo, error) {
	ch := c.subscribe(0) // file responses come on chanID 0
	defer c.unsubscribe(0, ch)
	c.send(ftFileList, 0, []byte(path))

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case f := <-ch:
			if f.Type != ftFileResp {
				continue
			}
			var files []FileInfo
			if err := json.Unmarshal(f.Payload, &files); err != nil {
				// Might be an error object.
				var errResp map[string]string
				if json.Unmarshal(f.Payload, &errResp) == nil {
					return nil, fmt.Errorf("%s", errResp["error"])
				}
				return nil, fmt.Errorf("decode file list: %w", err)
			}
			return files, nil
		case <-deadline.C:
			return nil, fmt.Errorf("timeout waiting for file list response")
		}
	}
}

// ---- Misc helpers ----------------------------------------------------------

// printableE2E strips control characters from terminal output.
func printableE2E(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		if (r >= 32 && r < 127) || r == '\n' || r == '\r' || r == '\t' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// syncSession drains any session-sync frames after connecting.
// Returns the parsed ptyChannels from the sync payload.
func (c *WSClient) syncSession(timeout time.Duration) []map[string]any {
	ch := c.subscribe(0xFFFF) // all frames
	defer c.unsubscribe(0xFFFF, ch)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var channels []map[string]any
	for {
		select {
		case f := <-ch:
			if f.Type == ftSessionSync {
				var payload struct {
					PTYChannels []map[string]any `json:"ptyChannels"`
				}
				json.Unmarshal(f.Payload, &payload) //nolint:errcheck
				channels = payload.PTYChannels
				return channels
			}
		case <-deadline.C:
			return channels
		}
	}
}
