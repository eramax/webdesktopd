package stats

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"webdesktopd/internal/hub"
)

// mockSender captures the last frame sent to it.
type mockSender struct {
	mu    sync.Mutex
	frames []hub.Frame
}

func (m *mockSender) Send(f hub.Frame) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frames = append(m.frames, f)
	return nil
}

func (m *mockSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.frames)
}

func (m *mockSender) last() hub.Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.frames[len(m.frames)-1]
}

func TestCollectorStartsAndStops(t *testing.T) {
	c := New()
	s := &mockSender{}

	id := c.Add(s)

	// Wait for at least one tick.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.count() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s.count() == 0 {
		t.Fatal("no stats frame received within 3s")
	}

	c.Remove(id)

	// After removal the loop should stop; count should not grow.
	before := s.count()
	time.Sleep(1500 * time.Millisecond)
	after := s.count()
	// Allow at most one extra frame that was in-flight.
	if after-before > 1 {
		t.Errorf("collector kept running after Remove: before=%d after=%d", before, after)
	}
}

func TestCollectorPayload(t *testing.T) {
	c := New()
	s := &mockSender{}
	id := c.Add(s)
	defer c.Remove(id)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.count() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s.count() == 0 {
		t.Fatal("no stats frame received within 3s")
	}

	f := s.last()
	if f.Type != hub.FrameStats {
		t.Fatalf("expected FrameStats (0x03), got 0x%02x", f.Type)
	}
	if f.ChanID != 0 {
		t.Errorf("expected chanID 0, got %d", f.ChanID)
	}

	var snap Snapshot
	if err := json.Unmarshal(f.Payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snap.RAMTotal == 0 {
		t.Error("RAMTotal is 0")
	}
	if snap.DiskTotal == 0 {
		t.Error("DiskTotal is 0")
	}
	if snap.Uptime <= 0 {
		t.Error("Uptime is not positive")
	}
	if len(snap.LoadAvg) != 3 {
		t.Errorf("LoadAvg length: want 3, got %d", len(snap.LoadAvg))
	}
	if snap.CPU < 0 || snap.CPU > 100 {
		t.Errorf("CPU out of range: %f", snap.CPU)
	}
}

func TestCollectorMultipleSenders(t *testing.T) {
	c := New()
	s1 := &mockSender{}
	s2 := &mockSender{}

	id1 := c.Add(s1)
	id2 := c.Add(s2)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s1.count() > 0 && s2.count() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s1.count() == 0 || s2.count() == 0 {
		t.Fatalf("both senders should receive frames: s1=%d s2=%d", s1.count(), s2.count())
	}

	// Remove one sender; loop must keep running for the other.
	c.Remove(id1)
	before := s2.count()
	time.Sleep(1500 * time.Millisecond)
	if s2.count() <= before {
		t.Error("collector stopped prematurely after removing first sender")
	}

	c.Remove(id2)
}
