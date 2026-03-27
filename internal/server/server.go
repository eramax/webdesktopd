package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"os/user"

	"github.com/gorilla/websocket"
	"webdesktopd/internal/auth"
	"webdesktopd/internal/hub"
	ptySession "webdesktopd/internal/pty"
	"webdesktopd/internal/stats"
)

// Config holds server configuration.
type Config struct {
	Addr      string
	JWTSecret []byte
	SSHAddr   string
	JWTTTL    time.Duration
}

// PTYInfo describes an active PTY channel in the session sync payload.
type PTYInfo struct {
	ChanID   uint16 `json:"chanID"`
	Username string `json:"username"`
}

// SessionSyncPayload is sent to the client on (re)connect.
type SessionSyncPayload struct {
	PTYChannels []PTYInfo `json:"ptyChannels"`
	HomeDir     string    `json:"homeDir"`
}

// UserSession holds all active PTY sessions for a single user.
type UserSession struct {
	Username string
	ptys     map[uint16]*ptySession.Session
	mu       sync.Mutex
}

func newUserSession(username string) *UserSession {
	return &UserSession{
		Username: username,
		ptys:     make(map[uint16]*ptySession.Session),
	}
}

// addPTY adds a PTY session keyed by its channel ID.
func (us *UserSession) addPTY(s *ptySession.Session) {
	us.mu.Lock()
	defer us.mu.Unlock()
	us.ptys[s.ChanID] = s
}

// removePTY removes the PTY session for the given channel and closes it.
func (us *UserSession) removePTY(chanID uint16) {
	us.mu.Lock()
	s, ok := us.ptys[chanID]
	if ok {
		delete(us.ptys, chanID)
	}
	us.mu.Unlock()
	if ok {
		s.Close()
	}
}

// registerHandlers registers existing PTY sessions as channel handlers so that
// client-to-server input frames are routed correctly. It does NOT replay ring
// buffers — that happens when the client sends an explicit OpenPTY frame.
func (us *UserSession) registerHandlers(h *hub.Hub) {
	us.mu.Lock()
	defer us.mu.Unlock()
	for _, s := range us.ptys {
		h.Register(s.ChanID, s)
	}
}

// detachHub detaches the hub from all PTY sessions.
func (us *UserSession) detachHub() {
	us.mu.Lock()
	ptys := make([]*ptySession.Session, 0, len(us.ptys))
	for _, s := range us.ptys {
		ptys = append(ptys, s)
	}
	us.mu.Unlock()

	for _, s := range ptys {
		s.Detach()
	}
}

// ptyInfoList returns the current set of PTY channel infos.
func (us *UserSession) ptyInfoList() []PTYInfo {
	us.mu.Lock()
	defer us.mu.Unlock()
	infos := make([]PTYInfo, 0, len(us.ptys))
	for chanID := range us.ptys {
		infos = append(infos, PTYInfo{ChanID: chanID, Username: us.Username})
	}
	return infos
}

// Server is the main HTTP server.
type Server struct {
	cfg      Config
	auth     *auth.Authenticator
	sessions sync.Map // string (username) → *UserSession
	upgrader websocket.Upgrader
	assets   http.FileSystem // embedded frontend (nil = no static serving)
	stats    *stats.Collector
}

// New creates a new Server with the given configuration.
func New(cfg Config) *Server {
	return &Server{
		cfg:  cfg,
		auth: auth.New(cfg.SSHAddr, cfg.JWTSecret, cfg.JWTTTL),
		upgrader: websocket.Upgrader{
			CheckOrigin:     func(r *http.Request) bool { return true }, // permissive for dev
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
		},
		stats: stats.New(),
	}
}

// SetAssets sets the embedded frontend file system.
func (s *Server) SetAssets(fs http.FileSystem) {
	s.assets = fs
}

// Handler returns the HTTP mux with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", s.withCORS(s.handleAuth))
	mux.HandleFunc("/ws", s.withCORS(s.handleWS))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})
	// Serve embedded frontend for all other paths (SPA fallback).
	if s.assets != nil {
		mux.Handle("/", s.spaHandler())
	}
	return mux
}

// spaHandler serves the embedded SPA. Unknown paths fall back to index.html.
func (s *Server) spaHandler() http.Handler {
	fs := http.FileServer(s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the file exists in the embedded FS.
		f, err := s.assets.Open(r.URL.Path)
		if err != nil {
			// Serve index.html for SPA client-side routing.
			r2 := *r
			r2.URL.Path = "/"
			fs.ServeHTTP(w, &r2)
			return
		}
		f.Close()
		fs.ServeHTTP(w, r)
	})
}

// withCORS wraps a handler to add permissive CORS headers.
func (s *Server) withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

// writeJSON sends a JSON-encoded response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writeJSON: encode error", "err", err)
	}
}

// writeError sends a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// authRequest is the JSON body for POST /auth.
type authRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	PrivateKeyPEM string `json:"privateKeyPem"`
}

// authResponse is the JSON response for POST /auth.
type authResponse struct {
	Token string `json:"token"`
}

// handleAuth handles POST /auth.
// Body: {"username":"...","password":"...","privateKeyPem":"..."}
// Response: {"token":"..."}
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var (
		token string
		err   error
	)

	switch {
	case req.PrivateKeyPEM != "":
		token, err = s.auth.AuthenticateKey(ctx, req.Username, []byte(req.PrivateKeyPEM))
	case req.Password != "":
		token, err = s.auth.Authenticate(ctx, req.Username, req.Password)
	default:
		writeError(w, http.StatusBadRequest, "password or privateKeyPem is required")
		return
	}

	if err != nil {
		slog.Warn("auth failed", "username", req.Username, "err", err)
		writeError(w, http.StatusUnauthorized, "authentication failed")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token})
}

// handleWS handles GET /ws?token=JWT.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		// Also check Authorization header as fallback.
		tokenStr = r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(tokenStr) > len(prefix) {
			tokenStr = tokenStr[len(prefix):]
		} else {
			tokenStr = ""
		}
	}

	if tokenStr == "" {
		writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	username, err := s.auth.ValidateToken(tokenStr)
	if err != nil {
		slog.Warn("ws: invalid token", "err", err)
		writeError(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("ws: upgrade error", "err", err)
		return
	}

	// Get or create the UserSession for this user.
	val, _ := s.sessions.LoadOrStore(username, newUserSession(username))
	us := val.(*UserSession)

	h := hub.New(conn)

	// Register with the stats collector. The collector starts its loop on the
	// first registration and stops when the last client disconnects.
	statsID := s.stats.Add(h)
	defer s.stats.Remove(statsID)

	// Register the control handler on chanID 0.
	ctrl := &controlHandler{
		server:      s,
		userSession: us,
		h:           h,
		username:    username,
	}
	h.Register(0, ctrl)

	// Register existing PTY sessions as input handlers. Ring buffer replay happens
	// when the client sends an explicit OpenPTY frame (idempotent re-attach).
	us.registerHandlers(h)

	// Resolve user home directory for the session sync payload.
	// Fall back to the conventional path when the user is not present in the
	// local passwd database (e.g. remote-only SSH users in embedded test mode).
	homeDir := ""
	if u, err := user.Lookup(username); err == nil {
		homeDir = u.HomeDir
	} else if username == "root" {
		homeDir = "/root"
	} else {
		homeDir = "/home/" + username
	}

	// Send session sync frame.
	syncPayload := SessionSyncPayload{
		PTYChannels: us.ptyInfoList(),
		HomeDir:     homeDir,
	}
	syncData, err := json.Marshal(syncPayload)
	if err != nil {
		slog.Warn("ws: marshal session sync", "err", err)
	} else {
		h.Send(hub.Frame{ //nolint:errcheck
			Type:    hub.FrameSessionSync,
			ChanID:  0,
			Payload: syncData,
		})
	}

	slog.Info("ws: client connected", "username", username, "remote", conn.RemoteAddr())

	// Run blocks until the connection closes or ctx is cancelled.
	if err := h.Run(r.Context()); err != nil {
		slog.Debug("ws: connection closed", "username", username, "err", err)
	}

	// Detach hub from all PTY sessions so they continue into ring buffers.
	us.detachHub()

	slog.Info("ws: client disconnected", "username", username)
}

// controlHandler handles frames on chanID 0 and acts as the default control plane.
type controlHandler struct {
	server      *Server
	userSession *UserSession
	h           *hub.Hub
	username    string
}

func (c *controlHandler) HandleFrame(ctx context.Context, f hub.Frame) error {
	switch f.Type {
	case hub.FrameOpenPTY:
		return c.handleOpenPTY(ctx, f)
	case hub.FrameClosePTY:
		return c.handleClosePTY(ctx, f)
	case hub.FramePing:
		// Echo back as Pong.
		return c.h.Send(hub.Frame{
			Type:    hub.FramePong,
			ChanID:  0,
			Payload: f.Payload,
		})
	case hub.FrameFileList:
		return c.handleFileList(ctx, f)
	case hub.FrameFileDownloadReq:
		return c.handleFileDownloadReq(ctx, f)
	case hub.FrameFileUpload:
		return c.handleFileUpload(ctx, f)
	case hub.FrameFileOp:
		return c.handleFileOp(ctx, f)
	case hub.FrameDesktopSave:
		return c.handleDesktopSave(ctx, f)
	default:
		slog.Debug("controlHandler: unhandled frame type", "type", f.Type)
	}
	return nil
}

func (c *controlHandler) Close() {
	// Nothing to clean up for the control handler itself.
}

// handleOpenPTY creates a new PTY session or re-attaches to an existing one.
// Re-attaching replays the ring buffer so reconnecting clients see missed output.
func (c *controlHandler) handleOpenPTY(ctx context.Context, f hub.Frame) error {
	var msg ptySession.OpenMsg
	if err := json.Unmarshal(f.Payload, &msg); err != nil {
		return fmt.Errorf("parse OpenPTY payload: %w", err)
	}
	if msg.Channel == 0 {
		return fmt.Errorf("chanID 0 is reserved for control")
	}

	// Check for an already-running session (reconnect path).
	c.userSession.mu.Lock()
	existing := c.userSession.ptys[msg.Channel]
	c.userSession.mu.Unlock()

	if existing != nil {
		// Re-attach: register for input routing and replay ring buffer.
		c.h.Register(msg.Channel, existing)
		existing.Attach(c.h)
		if msg.Cols > 0 && msg.Rows > 0 {
			existing.Resize(msg.Cols, msg.Rows)
		}
		slog.Info("controlHandler: re-attached PTY", "chanID", msg.Channel, "user", c.username)
		return nil
	}

	sess, err := ptySession.New(msg.Channel, c.username, msg.Shell, msg.CWD)
	if err != nil {
		slog.Warn("controlHandler: open PTY failed", "err", err, "user", c.username)
		return c.h.Send(hub.Frame{
			Type:    hub.FrameData,
			ChanID:  msg.Channel,
			Payload: []byte(fmt.Sprintf("error: %v\r\n", err)),
		})
	}

	if msg.Cols > 0 && msg.Rows > 0 {
		sess.Resize(msg.Cols, msg.Rows)
	}
	c.userSession.addPTY(sess)
	c.h.Register(msg.Channel, sess)
	sess.Attach(c.h)

	slog.Info("controlHandler: opened PTY", "chanID", msg.Channel, "user", c.username)
	return nil
}

// handleClosePTY closes an existing PTY session.
func (c *controlHandler) handleClosePTY(ctx context.Context, f hub.Frame) error {
	var msg struct {
		Channel uint16 `json:"channel"`
	}
	if err := json.Unmarshal(f.Payload, &msg); err != nil {
		return fmt.Errorf("parse ClosePTY payload: %w", err)
	}

	c.h.Unregister(msg.Channel)
	c.userSession.removePTY(msg.Channel)
	slog.Info("controlHandler: closed PTY", "chanID", msg.Channel, "user", c.username)
	return nil
}

// FileInfo represents a single file or directory entry.
type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	Mode    string `json:"mode"`
	ModTime string `json:"modTime"`
}

// FileListResponse is the JSON payload for FrameFileListResp.
type FileListResponse struct {
	Path    string     `json:"path"`
	Entries []FileInfo `json:"entries,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// handleFileList handles FrameFileList (0x04): list a directory.
// Request payload: raw UTF-8 path bytes.
// Response: FileListResponse JSON.
func (c *controlHandler) handleFileList(ctx context.Context, f hub.Frame) error {
	path := string(f.Payload)
	if path == "" {
		path = "/"
	}

	resp := FileListResponse{Path: path}
	entries, err := listDirectory(path)
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Entries = entries
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal file list: %w", err)
	}
	return c.h.Send(hub.Frame{
		Type:    hub.FrameFileListResp,
		ChanID:  f.ChanID,
		Payload: data,
	})
}

// handleFileDownloadReq handles FrameFileDownloadReq (0x08): send file in chunks.
func (c *controlHandler) handleFileDownloadReq(ctx context.Context, f hub.Frame) error {
	var req struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(f.Payload, &req); err != nil {
		return fmt.Errorf("parse download request: %w", err)
	}

	go func() {
		if err := streamFileDownload(ctx, c.h, f.ChanID, req.ID, req.Path); err != nil {
			slog.Warn("file download error", "path", req.Path, "err", err)
		}
	}()
	return nil
}

// handleFileUpload handles FrameFileUpload (0x06): receive and write file chunks.
func (c *controlHandler) handleFileUpload(ctx context.Context, f hub.Frame) error {
	// Payload format: [uploadID(36 bytes, UUID)|path_len(2 BE)|path|offset(8 BE)|data...]
	if len(f.Payload) < 36+2 {
		return fmt.Errorf("upload frame too short")
	}
	uploadID := string(f.Payload[:36])
	pathLen := int(f.Payload[36])<<8 | int(f.Payload[37])
	if len(f.Payload) < 36+2+pathLen+8 {
		return fmt.Errorf("upload frame truncated")
	}
	path := string(f.Payload[38 : 38+pathLen])
	offsetBytes := f.Payload[38+pathLen : 38+pathLen+8]
	offset := int64(offsetBytes[0])<<56 | int64(offsetBytes[1])<<48 |
		int64(offsetBytes[2])<<40 | int64(offsetBytes[3])<<32 |
		int64(offsetBytes[4])<<24 | int64(offsetBytes[5])<<16 |
		int64(offsetBytes[6])<<8 | int64(offsetBytes[7])
	data := f.Payload[38+pathLen+8:]

	if err := writeFileChunk(path, offset, data); err != nil {
		slog.Warn("file upload error", "path", path, "err", err)
		return nil
	}

	// Send progress frame.
	progress, _ := json.Marshal(map[string]any{
		"id":        uploadID,
		"bytesSent": offset + int64(len(data)),
	})
	return c.h.Send(hub.Frame{
		Type:    hub.FrameProgress,
		ChanID:  f.ChanID,
		Payload: progress,
	})
}

// handleFileOp handles FrameFileOp (0x11): rename/delete/chmod.
func (c *controlHandler) handleFileOp(ctx context.Context, f hub.Frame) error {
	var op struct {
		Op   string `json:"op"`
		Path string `json:"path"`
		Dst  string `json:"dst,omitempty"`
		Mode uint32 `json:"mode,omitempty"`
	}
	if err := json.Unmarshal(f.Payload, &op); err != nil {
		return fmt.Errorf("parse file op: %w", err)
	}

	switch op.Op {
	case "rename":
		if err := renameFile(op.Path, op.Dst); err != nil {
			slog.Warn("rename failed", "src", op.Path, "dst", op.Dst, "err", err)
		}
	case "delete":
		if err := deleteFile(op.Path); err != nil {
			slog.Warn("delete failed", "path", op.Path, "err", err)
		}
	case "chmod":
		if err := chmodFile(op.Path, op.Mode); err != nil {
			slog.Warn("chmod failed", "path", op.Path, "mode", op.Mode, "err", err)
		}
	case "mkdir":
		if err := mkdirPath(op.Path); err != nil {
			slog.Warn("mkdir failed", "path", op.Path, "err", err)
		}
	case "touch":
		if err := touchFile(op.Path); err != nil {
			slog.Warn("touch failed", "path", op.Path, "err", err)
		}
	case "copy":
		if err := copyPath(op.Path, op.Dst); err != nil {
			slog.Warn("copy failed", "src", op.Path, "dst", op.Dst, "err", err)
		}
	default:
		slog.Warn("unknown file op", "op", op.Op)
	}
	return nil
}

// handleDesktopSave handles FrameDesktopSave (0x13): persist desktop state.
func (c *controlHandler) handleDesktopSave(ctx context.Context, f hub.Frame) error {
	if err := saveDesktopState(c.username, f.Payload); err != nil {
		slog.Warn("save desktop state error", "user", c.username, "err", err)
	}
	return nil
}
