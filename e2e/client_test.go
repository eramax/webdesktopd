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
	ftPortScan    byte = 0x14
	ftPortScanResp byte = 0x15
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

// fileListResponse mirrors the server's FileListResponse struct.
type fileListResponse struct {
	Path    string     `json:"path"`
	Entries []FileInfo `json:"entries"`
	Error   string     `json:"error"`
}

// listDir sends FrameFileList and waits for a FrameFileListResp for that path.
func (c *WSClient) listDir(path string, timeout time.Duration) ([]FileInfo, error) {
	ch := c.subscribe(0)
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
			var resp fileListResponse
			if err := json.Unmarshal(f.Payload, &resp); err != nil {
				return nil, fmt.Errorf("decode file list resp: %w", err)
			}
			if resp.Path != path {
				continue // stale response for a different path
			}
			if resp.Error != "" {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return resp.Entries, nil
		case <-deadline.C:
			return nil, fmt.Errorf("timeout waiting for file list response for %q", path)
		}
	}
}

// fileOp sends a FrameFileOp (0x11) control message.
func (c *WSClient) fileOp(op, path, dst string) {
	c.sendJSON(ftFileOp, 0, map[string]any{
		"op":   op,
		"path": path,
		"dst":  dst,
		"mode": 0,
	})
}

// uploadFile sends a file's content in 64 KB chunks via FrameFileUpload (0x06).
func (c *WSClient) uploadFile(path string, data []byte) {
	const chunkSize = 64 * 1024
	uploadID := fmt.Sprintf("%-36s", "e2e-upload-"+path)
	if len(uploadID) > 36 {
		uploadID = uploadID[:36]
	}
	idBytes := []byte(uploadID)
	pathBytes := []byte(path)

	for offset := 0; offset < len(data) || (len(data) == 0 && offset == 0); {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]

		// Wire format: uploadID(36) | pathLen(2 BE) | path | offset(8 BE) | data
		payload := make([]byte, 36+2+len(pathBytes)+8+len(chunk))
		copy(payload[0:36], idBytes)
		binary.BigEndian.PutUint16(payload[36:38], uint16(len(pathBytes)))
		copy(payload[38:38+len(pathBytes)], pathBytes)
		binary.BigEndian.PutUint64(payload[38+len(pathBytes):], uint64(offset))
		copy(payload[38+len(pathBytes)+8:], chunk)
		c.send(ftFileUpload, 0, payload)

		offset += len(chunk)
		if len(chunk) == 0 {
			break
		}
	}
}

// downloadFile sends FrameFileDownloadReq and collects all chunks into a []byte.
func (c *WSClient) downloadFile(id, path string, timeout time.Duration) ([]byte, error) {
	ch := c.subscribe(0)
	defer c.unsubscribe(0, ch)

	c.sendJSON(ftFileDownReq, 0, map[string]any{"id": id, "path": path})

	idPad := fmt.Sprintf("%-36s", id)
	chunks := map[int64][]byte{}
	var total int64
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case f := <-ch:
			if f.Type == ftProgress {
				var p struct {
					ID        string `json:"id"`
					BytesSent int64  `json:"bytesSent"`
					Total     int64  `json:"total"`
					Error     string `json:"error"`
				}
				if json.Unmarshal(f.Payload, &p) == nil && p.ID == id {
					if p.Error != "" {
						return nil, fmt.Errorf("download error: %s", p.Error)
					}
					total = p.Total
					if p.Total > 0 && p.BytesSent >= p.Total {
						// Reassemble
						out := make([]byte, total)
						for off, chunk := range chunks {
							copy(out[off:], chunk)
						}
						return out, nil
					}
				}
			}
			if f.Type == ftFileDown && len(f.Payload) >= 44 {
				gotID := strings.TrimRight(string(f.Payload[:36]), " ")
				if gotID == id || string(f.Payload[:36]) == idPad {
					offset := int64(binary.BigEndian.Uint64(f.Payload[36:44]))
					chunks[offset] = append([]byte(nil), f.Payload[44:]...)
				}
			}
		case <-deadline.C:
			return nil, fmt.Errorf("timeout waiting for download of %q", path)
		}
	}
}

// ---- Port scan helpers -----------------------------------------------------

// PortScanEntry matches the server's PortInfo JSON.
type PortScanEntry struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	Cmdline string `json:"cmdline"`
}

// scanPorts sends FramePortScan and waits for a FramePortScanResp.
func (c *WSClient) scanPorts(timeout time.Duration) ([]PortScanEntry, error) {
	ch := c.subscribe(0xFFFF) // all frames
	defer c.unsubscribe(0xFFFF, ch)

	c.sendJSON(ftPortScan, 0, map[string]any{})

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case f := <-ch:
			if f.Type != ftPortScanResp {
				continue
			}
			var resp struct {
				Ports []PortScanEntry `json:"ports"`
			}
			if err := json.Unmarshal(f.Payload, &resp); err != nil {
				return nil, fmt.Errorf("decode port scan resp: %w", err)
			}
			return resp.Ports, nil
		case <-deadline.C:
			return nil, fmt.Errorf("timeout waiting for port scan response")
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

// sessionSyncResult holds the parsed session sync payload.
type sessionSyncResult struct {
	PTYChannels   []map[string]any
	ProxyChannels []map[string]any
	HomeDir       string
	DesktopState  json.RawMessage
}

// syncSession drains any session-sync frames after connecting.
func (c *WSClient) syncSession(timeout time.Duration) sessionSyncResult {
	ch := c.subscribe(0xFFFF) // all frames
	defer c.unsubscribe(0xFFFF, ch)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var result sessionSyncResult
	for {
		select {
		case f := <-ch:
			if f.Type == ftSessionSync {
				var payload struct {
					PTYChannels   []map[string]any `json:"ptyChannels"`
					ProxyChannels []map[string]any `json:"proxyChannels"`
					HomeDir       string           `json:"homeDir"`
					DesktopState  json.RawMessage  `json:"desktopState"`
				}
				json.Unmarshal(f.Payload, &payload) //nolint:errcheck
				result.PTYChannels = payload.PTYChannels
				result.ProxyChannels = payload.ProxyChannels
				result.HomeDir = payload.HomeDir
				result.DesktopState = payload.DesktopState
				return result
			}
		case <-deadline.C:
			return result
		}
	}
}
