package e2e

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDesktopStateSaveLoad(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(3 * time.Second)

	// Save desktop state via 0x13.
	state := map[string]any{
		"wallpaper": "linear-gradient(135deg,#0f172a,#1e3a5f)",
		"windows":   []any{},
		"tabs":      []any{},
	}
	stateJSON, _ := json.Marshal(state)
	c.send(ftDeskSave, 0, stateJSON)

	// Brief pause to let server write state.json.
	time.Sleep(200 * time.Millisecond)

	// Reconnect and verify state is returned in session sync.
	c.Close()
	c2 := dial(t, token)
	defer c2.Close()

	result := c2.syncSession(3 * time.Second)
	if result.DesktopState == nil {
		t.Fatal("desktopState not returned in session sync after save")
	}
	var loaded map[string]any
	if err := json.Unmarshal(result.DesktopState, &loaded); err != nil {
		t.Fatalf("unmarshal desktopState: %v", err)
	}
	got, _ := loaded["wallpaper"].(string)
	want := state["wallpaper"].(string)
	if got != want {
		t.Errorf("wallpaper: got %q, want %q", got, want)
	}
}

func TestDesktopStatePersistsAcrossReconnects(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(3 * time.Second)

	// Save a distinct wallpaper.
	stateJSON, _ := json.Marshal(map[string]any{
		"wallpaper": "linear-gradient(135deg,#064e3b,#065f46)",
	})
	c.send(ftDeskSave, 0, stateJSON)
	time.Sleep(200 * time.Millisecond)
	c.Close()

	// Two reconnects; state should persist both times.
	for i := 0; i < 2; i++ {
		c = dial(t, token)
		result := c.syncSession(3 * time.Second)
		c.Close()
		if result.DesktopState == nil {
			t.Fatalf("reconnect %d: desktopState missing", i+1)
		}
		var s map[string]any
		json.Unmarshal(result.DesktopState, &s) //nolint:errcheck
		if got, _ := s["wallpaper"].(string); got == "" {
			t.Fatalf("reconnect %d: wallpaper missing from state", i+1)
		}
	}
}
