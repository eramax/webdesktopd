package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"webdesktopd/internal/hub"
)

// PortProxySession tunnels raw TCP data over a WebSocket channel.
// Opening: client sends 0x0F with target; server dials TCP and relays 0x01 frames.
// Closing: client sends 0x10, or TCP connection closes (server sends 0x10 S→C).
type PortProxySession struct {
	ChanID uint16
	Target string

	hubMu sync.Mutex
	h     *hub.Hub

	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

// newPortProxySession dials the TCP target and starts the read goroutine.
func newPortProxySession(chanID uint16, target string, h *hub.Hub) (*PortProxySession, error) {
	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %q: %w", target, err)
	}
	ps := &PortProxySession{
		ChanID: chanID,
		Target: target,
		h:      h,
		conn:   conn,
		closed: make(chan struct{}),
	}
	go ps.readLoop()
	return ps, nil
}

// Attach updates the hub used to send frames to the client.
func (ps *PortProxySession) Attach(h *hub.Hub) {
	ps.hubMu.Lock()
	ps.h = h
	ps.hubMu.Unlock()
}

// Detach removes the hub reference so output is dropped until re-attached.
func (ps *PortProxySession) Detach() {
	ps.hubMu.Lock()
	ps.h = nil
	ps.hubMu.Unlock()
}

func (ps *PortProxySession) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := ps.conn.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			ps.hubMu.Lock()
			h := ps.h
			ps.hubMu.Unlock()
			if h != nil {
				h.Send(hub.Frame{ //nolint:errcheck
					Type:    hub.FrameData,
					ChanID:  ps.ChanID,
					Payload: data,
				})
			}
		}
		if err != nil {
			break
		}
	}
	// Notify client that TCP connection is closed.
	ps.hubMu.Lock()
	h := ps.h
	ps.hubMu.Unlock()
	if h != nil {
		payload, _ := json.Marshal(map[string]any{"channel": ps.ChanID})
		h.Send(hub.Frame{ //nolint:errcheck
			Type:    hub.FrameCloseProxy,
			ChanID:  ps.ChanID,
			Payload: payload,
		})
	}
	ps.closeConn()
}

// HandleFrame implements hub.ChannelHandler.
// Data frames (0x01) are written to the TCP connection.
func (ps *PortProxySession) HandleFrame(ctx context.Context, f hub.Frame) error {
	if f.Type == hub.FrameData && len(f.Payload) > 0 {
		if _, err := ps.conn.Write(f.Payload); err != nil {
			return fmt.Errorf("proxy write to %q: %w", ps.Target, err)
		}
	}
	return nil
}

// Close implements hub.ChannelHandler.
func (ps *PortProxySession) Close() {
	ps.closeConn()
}

func (ps *PortProxySession) closeConn() {
	ps.once.Do(func() {
		close(ps.closed)
		ps.conn.Close()
		slog.Debug("proxy: TCP connection closed", "chanID", ps.ChanID, "target", ps.Target)
	})
}
