package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"webdesktopd/internal/hub"
)

const downloadChunkSize = 64 * 1024 // 64KB

// listDirectory returns a slice of FileInfo for the given path.
func listDirectory(path string) ([]FileInfo, error) {
	// Clean and validate path.
	path = filepath.Clean(path)

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read dir %q: %w", path, err)
	}

	infos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			slog.Warn("file list: stat error", "name", entry.Name(), "err", err)
			continue
		}
		infos = append(infos, FileInfo{
			Name:    entry.Name(),
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			Mode:    fmt.Sprintf("%04o", info.Mode().Perm()),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return infos, nil
}

// streamFileDownload reads a file and sends it as FrameFileDownload chunks to the hub.
func streamFileDownload(ctx context.Context, h *hub.Hub, chanID uint16, downloadID, path string) error {
	path = filepath.Clean(path)
	f, err := os.Open(path)
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error(), "id": downloadID})
		h.Send(hub.Frame{Type: hub.FrameProgress, ChanID: chanID, Payload: errData}) //nolint:errcheck
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	total := stat.Size()

	// Payload for FrameFileDownload: [downloadID(36)|offset(8)|data...]
	idBytes := []byte(downloadID)
	if len(idBytes) > 36 {
		idBytes = idBytes[:36]
	}
	// Pad to 36 bytes.
	for len(idBytes) < 36 {
		idBytes = append(idBytes, ' ')
	}

	buf := make([]byte, downloadChunkSize)
	var offset int64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, 36+8+n)
			copy(chunk[:36], idBytes)
			putInt64BE(chunk[36:44], offset)
			copy(chunk[44:], buf[:n])

			if err := h.Send(hub.Frame{
				Type:    hub.FrameFileDownload,
				ChanID:  chanID,
				Payload: chunk,
			}); err != nil {
				return err
			}

			offset += int64(n)

			// Send progress frame.
			progress, _ := json.Marshal(map[string]any{
				"id":        downloadID,
				"bytesSent": offset,
				"total":     total,
			})
			h.Send(hub.Frame{Type: hub.FrameProgress, ChanID: chanID, Payload: progress}) //nolint:errcheck
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read %q: %w", path, readErr)
		}
	}
	return nil
}

// writeFileChunk writes data at the given offset in the destination file.
// If offset == 0, the file is created or truncated.
func writeFileChunk(path string, offset int64, data []byte) error {
	path = filepath.Clean(path)

	// Ensure parent directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdirall %q: %w", dir, err)
	}

	var flags int
	if offset == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else {
		flags = os.O_CREATE | os.O_WRONLY
	}

	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// renameFile renames src to dst atomically.
func renameFile(src, dst string) error {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)
	return os.Rename(src, dst)
}

// deleteFile removes a file or empty directory.
func deleteFile(path string) error {
	path = filepath.Clean(path)
	return os.Remove(path)
}

// chmodFile changes the file permissions.
func chmodFile(path string, mode uint32) error {
	path = filepath.Clean(path)
	return os.Chmod(path, os.FileMode(mode))
}

// DesktopState is the persisted desktop layout.
type DesktopState struct {
	Wallpaper string          `json:"wallpaper"`
	Windows   []WindowState   `json:"windows"`
	Tabs      []TerminalTabMeta `json:"tabs"`
}

// WindowState describes a single window's position and size.
type WindowState struct {
	ID     string  `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	ZIndex int     `json:"zIndex"`
}

// TerminalTabMeta holds metadata for a terminal tab.
type TerminalTabMeta struct {
	ChanID uint16 `json:"chanID"`
	Label  string `json:"label"`
}

// desktopStatePath returns the path to the desktop state file for a user.
func desktopStatePath(username string) (string, error) {
	// Look up the user's home directory.
	homeParts := []string{"/home", username}
	homeDir := filepath.Join(homeParts...)

	// Try os/user for accurate home directory.
	// We avoid importing os/user here to keep this file self-contained;
	// use the stat-based fallback.
	info, err := os.Stat(homeDir)
	if err != nil || !info.IsDir() {
		// Fallback: try /root for root user.
		if username == "root" {
			homeDir = "/root"
		} else {
			return "", fmt.Errorf("could not determine home directory for %q", username)
		}
	}

	stateDir := filepath.Join(homeDir, ".webdesktopd")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", fmt.Errorf("mkdirall %q: %w", stateDir, err)
	}
	return filepath.Join(stateDir, "state.json"), nil
}

// saveDesktopState writes the raw JSON state to ~/.webdesktopd/state.json.
func saveDesktopState(username string, stateJSON []byte) error {
	path, err := desktopStatePath(username)
	if err != nil {
		return err
	}
	// Write atomically via a temp file.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(stateJSON); err != nil {
		tmp.Close()
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// loadDesktopState reads ~/.webdesktopd/state.json for the given user.
// Returns nil, nil if the file does not exist.
func loadDesktopState(username string) ([]byte, error) {
	path, err := desktopStatePath(username)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		// Check for syscall.ENOENT explicitly (cross-platform).
		if pathErr, ok := err.(*os.PathError); ok {
			if pathErr.Err == syscall.ENOENT {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	return data, nil
}

// putInt64BE writes a 64-bit big-endian integer into dst (must be at least 8 bytes).
func putInt64BE(dst []byte, v int64) {
	dst[0] = byte(v >> 56)
	dst[1] = byte(v >> 48)
	dst[2] = byte(v >> 40)
	dst[3] = byte(v >> 32)
	dst[4] = byte(v >> 24)
	dst[5] = byte(v >> 16)
	dst[6] = byte(v >> 8)
	dst[7] = byte(v)
}
