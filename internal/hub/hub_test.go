package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// testPair creates a connected client/server WebSocket pair using httptest.
// Returns the server-side *websocket.Conn and the client-side *websocket.Conn.
func testPair(t *testing.T, serverHandler func(conn *websocket.Conn)) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverHandler(conn)
	}))
	t.Cleanup(srv.Close)

	url := "ws" + srv.URL[4:] // replace "http" with "ws"
	clientConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() })
	return clientConn
}

// mockHandler is a ChannelHandler that records received frames.
type mockHandler struct {
	frames []Frame
	mu     sync.Mutex
	closed atomic.Bool
	onFrame func(f Frame)
}

func (m *mockHandler) HandleFrame(ctx context.Context, f Frame) error {
	m.mu.Lock()
	m.frames = append(m.frames, f)
	m.mu.Unlock()
	if m.onFrame != nil {
		m.onFrame(f)
	}
	return nil
}

func (m *mockHandler) Close() {
	m.closed.Store(true)
}

func (m *mockHandler) Frames() []Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Frame, len(m.frames))
	copy(out, m.frames)
	return out
}

// TestClientToServerDispatch verifies that a frame sent by the client reaches the correct handler.
func TestClientToServerDispatch(t *testing.T) {
	handler := &mockHandler{}
	received := make(chan struct{})
	handler.onFrame = func(f Frame) { close(received) }

	var hubDone sync.WaitGroup
	hubDone.Add(1)

	clientConn := testPair(t, func(conn *websocket.Conn) {
		defer hubDone.Done()
		h := New(conn)
		h.Register(1, handler)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Run(ctx) //nolint:errcheck
	})

	// Send a frame on chanID 1.
	f := Frame{Type: FrameData, ChanID: 1, Payload: []byte("hello")}
	if err := clientConn.WriteMessage(websocket.BinaryMessage, Encode(f)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame to be dispatched")
	}

	frames := handler.Frames()
	if len(frames) == 0 {
		t.Fatal("handler received no frames")
	}
	if frames[0].ChanID != 1 || string(frames[0].Payload) != "hello" {
		t.Errorf("unexpected frame: %+v", frames[0])
	}
}

// TestServerToClientSend verifies that hub.Send delivers a frame to the client.
func TestServerToClientSend(t *testing.T) {
	var h *Hub
	hubReady := make(chan struct{})

	clientConn := testPair(t, func(conn *websocket.Conn) {
		h = New(conn)
		close(hubReady)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Run(ctx) //nolint:errcheck
	})

	<-hubReady

	want := Frame{Type: FrameStats, ChanID: 0, Payload: []byte(`{"cpu":42}`)}
	if err := h.Send(want); err != nil {
		t.Fatalf("Send: %v", err)
	}

	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client ReadMessage: %v", err)
	}

	got, err := decodeFromBytes(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Type != want.Type {
		t.Errorf("Type: got %02x, want %02x", got.Type, want.Type)
	}
	if string(got.Payload) != string(want.Payload) {
		t.Errorf("Payload: got %q, want %q", got.Payload, want.Payload)
	}
}

// TestMultipleChannelRouting verifies that frames are routed to the correct handler.
func TestMultipleChannelRouting(t *testing.T) {
	handler1 := &mockHandler{}
	handler2 := &mockHandler{}
	received := make(chan struct{}, 2)
	handler1.onFrame = func(f Frame) { received <- struct{}{} }
	handler2.onFrame = func(f Frame) { received <- struct{}{} }

	clientConn := testPair(t, func(conn *websocket.Conn) {
		h := New(conn)
		h.Register(1, handler1)
		h.Register(2, handler2)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Run(ctx) //nolint:errcheck
	})

	// Send to chan 1.
	f1 := Frame{Type: FrameData, ChanID: 1, Payload: []byte("for-one")}
	clientConn.WriteMessage(websocket.BinaryMessage, Encode(f1)) //nolint:errcheck

	// Send to chan 2.
	f2 := Frame{Type: FrameData, ChanID: 2, Payload: []byte("for-two")}
	clientConn.WriteMessage(websocket.BinaryMessage, Encode(f2)) //nolint:errcheck

	for i := 0; i < 2; i++ {
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for frames")
		}
	}

	frames1 := handler1.Frames()
	frames2 := handler2.Frames()

	if len(frames1) != 1 || string(frames1[0].Payload) != "for-one" {
		t.Errorf("handler1: unexpected frames: %+v", frames1)
	}
	if len(frames2) != 1 || string(frames2[0].Payload) != "for-two" {
		t.Errorf("handler2: unexpected frames: %+v", frames2)
	}
}

// TestUnregisterCallsClose verifies that Unregister calls Close() on the handler.
func TestUnregisterCallsClose(t *testing.T) {
	handler := &mockHandler{}

	clientConn := testPair(t, func(conn *websocket.Conn) {
		h := New(conn)
		h.Register(5, handler)
		h.Unregister(5)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		h.Run(ctx) //nolint:errcheck
	})
	_ = clientConn

	// Give the server goroutine time to run.
	time.Sleep(200 * time.Millisecond)

	if !handler.closed.Load() {
		t.Error("handler.Close() was not called after Unregister")
	}
}

// TestContextCancellationStopsRun verifies that cancelling the context causes Run to return.
func TestContextCancellationStopsRun(t *testing.T) {
	runDone := make(chan error, 1)

	clientConn := testPair(t, func(conn *websocket.Conn) {
		h := New(conn)
		ctx, cancel := context.WithCancel(context.Background())
		// Cancel after a short delay.
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
		err := h.Run(ctx)
		runDone <- err
	})
	_ = clientConn

	select {
	case <-runDone:
		// Run returned, as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestHubClosedSendReturnsError verifies that Send on a closed hub returns an error.
func TestHubClosedSendReturnsError(t *testing.T) {
	clientConn := testPair(t, func(conn *websocket.Conn) {
		h := New(conn)
		h.Close()
		err := h.Send(Frame{Type: FramePing, ChanID: 0})
		if err == nil {
			t.Error("expected error from Send on closed hub, got nil")
		}
	})
	_ = clientConn
	time.Sleep(100 * time.Millisecond)
}
