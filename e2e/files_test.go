package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestFileListHome verifies listing the user's home directory.
func TestFileListHome(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	home := fmt.Sprintf("/home/%s", cfg.User)
	files, err := c.listDir(home, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(%q): %v", home, err)
	}
	if len(files) == 0 {
		t.Fatalf("expected non-empty directory listing for %q", home)
	}
	t.Logf("✓ %q: %d entries", home, len(files))
	for i, f := range files {
		if i >= 8 {
			t.Logf("  … (%d more)", len(files)-8)
			break
		}
		t.Logf("  %s %s", f.Mode, f.Name)
	}
}

// TestFileListRoot verifies listing a system directory.
func TestFileListRoot(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	files, err := c.listDir("/tmp", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(/tmp): %v", err)
	}
	t.Logf("✓ /tmp: %d entries", len(files))
}

// TestFileListMetadata verifies that each FileInfo entry has required fields.
func TestFileListMetadata(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	home := fmt.Sprintf("/home/%s", cfg.User)
	files, err := c.listDir(home, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(%q): %v", home, err)
	}
	for _, f := range files {
		if f.Name == "" {
			t.Errorf("entry has empty Name: %+v", f)
		}
		if f.Mode == "" {
			t.Errorf("entry %q has empty Mode", f.Name)
		}
		if f.ModTime == "" {
			t.Errorf("entry %q has empty ModTime", f.Name)
		}
	}
	t.Log("✓ all entries have Name, Mode, ModTime")
}

// TestFileListNonExistent verifies that a non-existent path returns an error.
func TestFileListNonExistent(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	_, err := c.listDir("/nonexistent/path/e2e_test_xyz", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
	t.Logf("✓ non-existent path correctly returns error: %v", err)
}

// TestFileListEtc verifies that listing /etc works (read-accessible to all users).
func TestFileListEtc(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	files, err := c.listDir("/etc", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(/etc): %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected entries in /etc")
	}
	// Verify well-known files exist.
	names := make(map[string]bool, len(files))
	for _, f := range files {
		names[f.Name] = true
	}
	for _, want := range []string{"passwd", "hostname"} {
		if !names[want] {
			t.Errorf("expected %q in /etc listing", want)
		}
	}
	t.Logf("✓ /etc: %d entries, found passwd and hostname", len(files))
}

// TestFileListIsDir verifies that directory entries have IsDir=true.
func TestFileListIsDir(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	files, err := c.listDir("/", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(/): %v", err)
	}
	// Well-known directories at root.
	dirs := map[string]bool{}
	for _, f := range files {
		dirs[f.Name] = f.IsDir
	}
	for _, want := range []string{"tmp", "etc", "home"} {
		isDir, found := dirs[want]
		if !found {
			t.Errorf("/%s not found in root listing", want)
			continue
		}
		if !isDir {
			t.Errorf("/%s should be a directory", want)
		}
	}
	t.Log("✓ directory entries have IsDir=true")
}

// TestFileListSorting is a soft check: entries should have sensible names.
func TestFileListSorting(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	files, err := c.listDir("/etc", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(/etc): %v", err)
	}
	for _, f := range files {
		if strings.ContainsAny(f.Name, "/\x00") {
			t.Errorf("filename contains illegal character: %q", f.Name)
		}
	}
	t.Logf("✓ %d entries, no illegal characters in names", len(files))
}
