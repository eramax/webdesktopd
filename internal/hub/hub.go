package hub

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeChanSize   = 256
	pingInterval    = 30 * time.Second
	pongWait        = 60 * time.Second
	writeWait       = 10 * time.Second
)

// ChannelHandler handles frames for a specific channel.
type ChannelHandler interface {
	HandleFrame(ctx context.Context, f Frame) error
	Close()
}

// Hub manages a single WebSocket connection with multiplexed channels.
type Hub struct {
	conn      *websocket.Conn
	handlers  map[uint16]ChannelHandler
	mu        sync.RWMutex
	writeCh   chan []byte // serialized frames to write
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a new Hub for the given WebSocket connection and starts the writer goroutine.
func New(conn *websocket.Conn) *Hub {
	h := &Hub{
		conn:     conn,
		handlers: make(map[uint16]ChannelHandler),
		writeCh:  make(chan []byte, writeChanSize),
		done:     make(chan struct{}),
	}
	go h.writer()
	return h
}

// Register associates a ChannelHandler with a channel ID.
func (h *Hub) Register(chanID uint16, handler ChannelHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[chanID] = handler
}

// Unregister removes the handler for a channel ID and calls Close() on it.
func (h *Hub) Unregister(chanID uint16) {
	h.mu.Lock()
	handler, ok := h.handlers[chanID]
	if ok {
		delete(h.handlers, chanID)
	}
	h.mu.Unlock()
	if ok {
		handler.Close()
	}
}

// Send encodes and queues a frame for writing.
// Non-blocking: drops the frame if writeCh is full.
// Returns an error if the hub is closed.
func (h *Hub) Send(f Frame) error {
	select {
	case <-h.done:
		return fmt.Errorf("hub is closed")
	default:
	}
	data := Encode(f)
	select {
	case h.writeCh <- data:
		return nil
	case <-h.done:
		return fmt.Errorf("hub is closed")
	default:
		// Channel full – drop frame to avoid blocking.
		slog.Warn("hub: write channel full, dropping frame", "type", f.Type, "chanID", f.ChanID)
		return nil
	}
}

// Run starts the read loop, blocking until the connection closes or ctx is cancelled.
// It dispatches incoming frames to registered handlers.
func (h *Hub) Run(ctx context.Context) error {
	defer h.Close()

	// Configure pong handler to reset read deadline.
	h.conn.SetReadDeadline(time.Now().Add(pongWait))
	h.conn.SetPongHandler(func(string) error {
		h.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	readErr := make(chan error, 1)
	go func() {
		for {
			msgType, data, err := h.conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			if msgType != websocket.BinaryMessage {
				// Ignore non-binary messages.
				continue
			}
			frame, err := decodeFromBytes(data)
			if err != nil {
				slog.Warn("hub: failed to decode frame", "err", err)
				continue
			}

			h.mu.RLock()
			handler, ok := h.handlers[frame.ChanID]
			h.mu.RUnlock()

			if !ok {
				slog.Debug("hub: no handler for chanID, discarding frame", "chanID", frame.ChanID, "type", frame.Type)
				continue
			}

			if err := handler.HandleFrame(ctx, frame); err != nil {
				slog.Warn("hub: handler error", "chanID", frame.ChanID, "err", err)
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-readErr:
		return err
	case <-h.done:
		return nil
	}
}

// Close shuts down the hub and all registered channels.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.done)
		h.conn.Close()
		// Keep registered handlers alive across disconnects.
		// The server detaches/re-attaches PTY and proxy sessions separately so
		// they can continue running while the WebSocket is gone.
		h.mu.Lock()
		h.handlers = make(map[uint16]ChannelHandler)
		h.mu.Unlock()
	})
}

// writer is the single goroutine responsible for writing messages to the WebSocket.
// It also sends periodic pings.
func (h *Hub) writer() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.done:
			// Send a close message before exiting.
			h.conn.WriteMessage( //nolint:errcheck
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			)
			return

		case data, ok := <-h.writeCh:
			if !ok {
				return
			}
			h.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := h.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				slog.Warn("hub: write error", "err", err)
				h.Close()
				return
			}

		case <-ticker.C:
			h.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := h.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Warn("hub: ping error", "err", err)
				h.Close()
				return
			}
		}
	}
}

// decodeFromBytes decodes a single frame from a byte slice.
// This is used instead of Decode(io.Reader) for WebSocket message payloads.
func decodeFromBytes(data []byte) (Frame, error) {
	if len(data) < headerSize {
		return Frame{}, fmt.Errorf("frame too short: %d bytes", len(data))
	}
	frameType := data[0]
	chanID := uint16(data[1])<<8 | uint16(data[2])
	payloadLen := uint32(data[3])<<24 | uint32(data[4])<<16 | uint32(data[5])<<8 | uint32(data[6])

	if payloadLen > maxPayloadSize {
		return Frame{}, fmt.Errorf("frame payload length %d exceeds maximum %d", payloadLen, maxPayloadSize)
	}
	if int(payloadLen) != len(data)-headerSize {
		return Frame{}, fmt.Errorf("frame payload length %d does not match data length %d", payloadLen, len(data)-headerSize)
	}

	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		copy(payload, data[headerSize:])
	}

	return Frame{
		Type:    frameType,
		ChanID:  chanID,
		Payload: payload,
	}, nil
}
