package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// tmpDir returns a unique path inside /tmp for the test to use.
func tmpDir(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("/tmp/e2e_%s_%d", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
}

// ── Listing ───────────────────────────────────────────────────────────────────

// TestFileListHome verifies listing the user's home directory.
func TestFileListHome(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	sync := c.syncSession(2 * time.Second)
	if sync.HomeDir == "" {
		t.Skip("homeDir not provided by server — skipping")
	}

	files, err := c.listDir(sync.HomeDir, 5*time.Second)
	if err != nil {
		// Home may not exist on the local machine in embedded test mode.
		t.Skipf("listDir(%q): %v — skipping (home may be on remote system)", sync.HomeDir, err)
	}
	t.Logf("✓ %q: %d entries", sync.HomeDir, len(files))
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
	sync := c.syncSession(2 * time.Second)
	if sync.HomeDir == "" {
		t.Skip("homeDir not provided — skipping")
	}

	files, err := c.listDir(sync.HomeDir, 5*time.Second)
	if err != nil {
		t.Skipf("listDir(%q): %v — skipping (home may be on remote system)", sync.HomeDir, err)
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

// TestFileListEtc verifies that listing /etc works.
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

// TestFileListNoIllegalChars verifies filenames contain no illegal characters.
func TestFileListNoIllegalChars(t *testing.T) {
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

// TestSessionSyncHomeDir verifies that the session sync frame includes homeDir.
func TestSessionSyncHomeDir(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	result := c.syncSession(3 * time.Second)
	if result.HomeDir == "" {
		t.Fatal("session sync: homeDir is empty")
	}
	expected := fmt.Sprintf("/home/%s", cfg.User)
	if result.HomeDir != expected && result.HomeDir != "/root" {
		t.Errorf("homeDir = %q, want %q", result.HomeDir, expected)
	}
	t.Logf("✓ homeDir = %q", result.HomeDir)
}

// ── mkdir ─────────────────────────────────────────────────────────────────────

// TestFileMkdir verifies creating a new directory.
func TestFileMkdir(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	time.Sleep(100 * time.Millisecond)

	files, err := c.listDir("/tmp", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(/tmp): %v", err)
	}
	base := dir[len("/tmp/"):]
	found := false
	for _, f := range files {
		if f.Name == base && f.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created directory %q not found in /tmp listing", base)
	}

	// Cleanup
	c.fileOp("delete", dir, "")
	t.Logf("✓ mkdir %q created successfully", dir)
}

// TestFileMkdirNested verifies that mkdir creates intermediate parents.
func TestFileMkdirNested(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	base := tmpDir(t)
	nested := base + "/a/b/c"
	c.fileOp("mkdir", nested, "")
	time.Sleep(150 * time.Millisecond)

	files, err := c.listDir(base+"/a/b", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(%q): %v", base+"/a/b", err)
	}
	found := false
	for _, f := range files {
		if f.Name == "c" && f.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("nested directory %q not found", nested)
	}

	// Cleanup
	c.fileOp("delete", base, "")
	t.Logf("✓ nested mkdir %q created successfully", nested)
}

// ── touch ─────────────────────────────────────────────────────────────────────

// TestFileTouch verifies creating an empty file.
func TestFileTouch(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	time.Sleep(50 * time.Millisecond)

	filePath := dir + "/hello.txt"
	c.fileOp("touch", filePath, "")
	time.Sleep(100 * time.Millisecond)

	files, err := c.listDir(dir, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir(%q): %v", dir, err)
	}
	found := false
	for _, f := range files {
		if f.Name == "hello.txt" && !f.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("touched file %q not found", filePath)
	}

	// Cleanup
	c.fileOp("delete", dir, "")
	t.Logf("✓ touch %q created file", filePath)
}

// ── upload + download ─────────────────────────────────────────────────────────

// TestFileUploadDownload verifies round-trip upload then download of a file.
func TestFileUploadDownload(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	time.Sleep(50 * time.Millisecond)

	want := []byte("hello from e2e test\n")
	filePath := dir + "/test.txt"
	c.uploadFile(filePath, want)
	time.Sleep(200 * time.Millisecond)

	// Verify file appears in listing with correct size.
	files, err := c.listDir(dir, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir after upload: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Name == "test.txt" {
			found = true
			if f.Size != int64(len(want)) {
				t.Errorf("size = %d, want %d", f.Size, len(want))
			}
			break
		}
	}
	if !found {
		t.Fatalf("uploaded file not in listing")
	}

	// Download and compare.
	got, err := c.downloadFile("dl-round-trip-1", filePath, 10*time.Second)
	if err != nil {
		t.Fatalf("downloadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("downloaded content = %q, want %q", got, want)
	}

	// Cleanup
	c.fileOp("delete", dir, "")
	t.Logf("✓ upload/download round-trip: %d bytes", len(want))
}

// TestFileUploadLarge verifies upload of a multi-chunk file (> 64 KB).
func TestFileUploadLarge(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	time.Sleep(50 * time.Millisecond)

	// 200 KB of data
	want := make([]byte, 200*1024)
	for i := range want {
		want[i] = byte(i % 256)
	}
	filePath := dir + "/large.bin"
	c.uploadFile(filePath, want)
	time.Sleep(500 * time.Millisecond)

	files, err := c.listDir(dir, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir after large upload: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Name == "large.bin" {
			found = true
			if f.Size != int64(len(want)) {
				t.Errorf("size = %d, want %d", f.Size, len(want))
			}
			break
		}
	}
	if !found {
		t.Fatalf("large uploaded file not in listing")
	}

	// Download and verify integrity.
	got, err := c.downloadFile("dl-large-1", filePath, 15*time.Second)
	if err != nil {
		t.Fatalf("downloadFile large: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("downloaded %d bytes, want %d", len(got), len(want))
	}
	for i, b := range got {
		if b != want[i] {
			t.Errorf("byte[%d] = %d, want %d", i, b, want[i])
			break
		}
	}

	// Cleanup
	c.fileOp("delete", dir, "")
	t.Logf("✓ large upload/download: %d bytes", len(want))
}

// ── rename ────────────────────────────────────────────────────────────────────

// TestFileRename verifies renaming a file.
func TestFileRename(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	c.fileOp("touch", dir+"/original.txt", "")
	time.Sleep(100 * time.Millisecond)

	c.fileOp("rename", dir+"/original.txt", dir+"/renamed.txt")
	time.Sleep(100 * time.Millisecond)

	files, err := c.listDir(dir, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir after rename: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
	}
	if names["original.txt"] {
		t.Error("original.txt should no longer exist after rename")
	}
	if !names["renamed.txt"] {
		t.Error("renamed.txt should exist after rename")
	}

	// Cleanup
	c.fileOp("delete", dir, "")
	t.Log("✓ rename original.txt → renamed.txt")
}

// ── copy ─────────────────────────────────────────────────────────────────────

// TestFileCopy verifies copying a file.
func TestFileCopy(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	content := []byte("copy-me content")
	c.uploadFile(dir+"/src.txt", content)
	time.Sleep(150 * time.Millisecond)

	c.fileOp("copy", dir+"/src.txt", dir+"/dst.txt")
	time.Sleep(150 * time.Millisecond)

	files, err := c.listDir(dir, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir after copy: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
	}
	if !names["src.txt"] {
		t.Error("src.txt should still exist after copy")
	}
	if !names["dst.txt"] {
		t.Error("dst.txt should exist after copy")
	}

	// Verify content of copy is identical.
	got, err := c.downloadFile("dl-copy-1", dir+"/dst.txt", 10*time.Second)
	if err != nil {
		t.Fatalf("download copy: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", got, content)
	}

	// Cleanup
	c.fileOp("delete", dir, "")
	t.Log("✓ file copy: src.txt → dst.txt, content verified")
}

// TestFileCopyDir verifies recursive directory copy.
func TestFileCopyDir(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	base := tmpDir(t)
	src := base + "/src"
	dst := base + "/dst"
	c.fileOp("mkdir", src, "")
	c.uploadFile(src+"/a.txt", []byte("file-a"))
	c.uploadFile(src+"/b.txt", []byte("file-b"))
	time.Sleep(200 * time.Millisecond)

	c.fileOp("copy", src, dst)
	time.Sleep(300 * time.Millisecond)

	files, err := c.listDir(dst, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir after dir copy: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] {
		t.Errorf("dst dir missing expected files, got: %v", names)
	}

	// Cleanup
	c.fileOp("delete", base, "")
	t.Log("✓ directory copy: src/{a.txt,b.txt} → dst/{a.txt,b.txt}")
}

// ── delete ────────────────────────────────────────────────────────────────────

// TestFileDelete verifies deleting a single file.
func TestFileDelete(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	dir := tmpDir(t)
	c.fileOp("mkdir", dir, "")
	c.fileOp("touch", dir+"/todelete.txt", "")
	time.Sleep(100 * time.Millisecond)

	c.fileOp("delete", dir+"/todelete.txt", "")
	time.Sleep(100 * time.Millisecond)

	files, err := c.listDir(dir, 5*time.Second)
	if err != nil {
		t.Fatalf("listDir after delete: %v", err)
	}
	for _, f := range files {
		if f.Name == "todelete.txt" {
			t.Error("deleted file still present")
		}
	}

	c.fileOp("delete", dir, "")
	t.Log("✓ file deleted successfully")
}

// TestFileDeleteDirectory verifies recursive directory deletion.
func TestFileDeleteDirectory(t *testing.T) {
	token := mustAuth(t, cfg.User, cfg.Pass)
	c := dial(t, token)
	c.syncSession(2 * time.Second)

	base := tmpDir(t)
	c.fileOp("mkdir", base+"/sub", "")
	c.uploadFile(base+"/sub/file.txt", []byte("data"))
	time.Sleep(200 * time.Millisecond)

	// Delete the entire base directory (non-empty).
	c.fileOp("delete", base, "")
	time.Sleep(150 * time.Millisecond)

	// The parent listing should no longer contain it.
	files, err := c.listDir("/tmp", 5*time.Second)
	if err != nil {
		t.Fatalf("listDir /tmp: %v", err)
	}
	baseName := base[len("/tmp/"):]
	for _, f := range files {
		if f.Name == baseName {
			t.Error("recursively-deleted directory still present in /tmp")
		}
	}
	t.Log("✓ non-empty directory deleted recursively")
}
