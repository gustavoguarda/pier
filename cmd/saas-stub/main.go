package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const responseWait = 30 * time.Second

const (
	defaultDataDir       = "/data"
	defaultRetentionDays = 30
	cleanupInterval      = 24 * time.Hour
	cleanupInitialDelay  = 5 * time.Minute
)

// fileEntry is the lookup record for a queued upload. Content lives on disk
// under <dataDir>/<client>/<filename>; we keep client/filename in memory just
// to translate file IDs (used in /files/<id> URLs) back to the disk path.
type fileEntry struct {
	Filename string
	Client   string
	DiskPath string
}

type server struct {
	mu             sync.Mutex
	files          map[string]fileEntry
	responses      map[string]chan []byte
	nextID         int
	agent          *websocket.Conn
	agentMu        sync.Mutex
	expectedToken  string
	dataDir        string
	retentionHours time.Duration
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	addr := flag.String("addr", "", "listen address (default :9000 or :$PORT)")
	token := flag.String("token", "", "expected agent and client token (default $STORAGE_SAAS_TOKEN or 'dev-token')")
	flag.Parse()

	// Resolve config: flag > env var > default. Lets the same binary serve
	// local dev (flags) and production (env vars, PaaS-style).
	resolvedAddr := *addr
	if resolvedAddr == "" {
		if envPort := os.Getenv("PORT"); envPort != "" {
			resolvedAddr = ":" + envPort
		} else {
			resolvedAddr = ":9000"
		}
	}

	resolvedToken := *token
	if resolvedToken == "" {
		if envToken := os.Getenv("STORAGE_SAAS_TOKEN"); envToken != "" {
			resolvedToken = envToken
		} else {
			resolvedToken = "dev-token"
		}
	}

	// Data dir resolution (env > default).
	resolvedDataDir := os.Getenv("STORAGE_DATA_DIR")
	if resolvedDataDir == "" {
		resolvedDataDir = defaultDataDir
	}
	if err := os.MkdirAll(resolvedDataDir, 0o755); err != nil {
		slog.Error("create data dir", "dir", resolvedDataDir, "error", err)
		os.Exit(1)
	}

	// Retention (env > default 30 days). Files older than this are deleted
	// by the background cleanup loop. Set RETENTION_DAYS=0 to disable cleanup.
	retentionDays := defaultRetentionDays
	if v := os.Getenv("RETENTION_DAYS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			retentionDays = parsed
		}
	}

	s := &server{
		files:          make(map[string]fileEntry),
		responses:      make(map[string]chan []byte),
		expectedToken:  resolvedToken,
		dataDir:        resolvedDataDir,
		retentionHours: time.Duration(retentionDays) * 24 * time.Hour,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent/ws", s.handleAgentWS)
	mux.HandleFunc("/files/", s.handleFile)
	mux.HandleFunc("/upload", s.handleUpload)
	mux.HandleFunc("/download", s.handleDownload)
	mux.HandleFunc("/agent/responses/", s.handleAgentResponse)
	mux.HandleFunc("/healthz", s.handleHealth)

	// Never log the token itself; just whether it was supplied via env.
	tokenSource := "default"
	if *token != "" {
		tokenSource = "flag"
	} else if os.Getenv("STORAGE_SAAS_TOKEN") != "" {
		tokenSource = "env"
	}
	slog.Info("saas-stub listening",
		"addr", resolvedAddr,
		"token_source", tokenSource,
		"data_dir", resolvedDataDir,
		"retention_days", retentionDays,
	)

	if retentionDays > 0 {
		go s.runCleanupLoop()
	} else {
		slog.Warn("retention disabled (RETENTION_DAYS=0) — files will accumulate forever")
	}

	if err := http.ListenAndServe(resolvedAddr, mux); err != nil {
		slog.Error("server failed", "error", err)
	}
}

// runCleanupLoop walks the data dir periodically and deletes files whose mtime
// is older than the retention window. Initial delay avoids hammering disk
// right after boot.
func (s *server) runCleanupLoop() {
	time.Sleep(cleanupInitialDelay)
	for {
		s.runCleanupOnce()
		time.Sleep(cleanupInterval)
	}
}

func (s *server) runCleanupOnce() {
	cutoff := time.Now().Add(-s.retentionHours)
	deleted := 0
	var freedBytes int64

	err := filepath.Walk(s.dataDir, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("cleanup walk error", "path", walkPath, "error", err)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		size := info.Size()
		if err := os.Remove(walkPath); err != nil {
			slog.Warn("cleanup delete failed", "path", walkPath, "error", err)
			return nil
		}
		deleted++
		freedBytes += size
		return nil
	})
	if err != nil {
		slog.Error("cleanup walk failed", "error", err)
		return
	}

	// Best-effort empty-directory cleanup so the tree doesn't grow boundlessly
	// with hollow client folders. Errors here are non-fatal.
	_ = filepath.Walk(s.dataDir, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || walkPath == s.dataDir {
			return nil
		}
		entries, readErr := os.ReadDir(walkPath)
		if readErr == nil && len(entries) == 0 {
			_ = os.Remove(walkPath)
		}
		return nil
	})

	if deleted > 0 {
		slog.Info("cleanup completed",
			"deleted_files", deleted,
			"freed_mb", fmt.Sprintf("%.2f", float64(freedBytes)/(1024*1024)),
			"retention_days", int(s.retentionHours.Hours()/24),
		)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.agentMu.Lock()
	connected := s.agent != nil
	s.agentMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if connected {
		fmt.Fprint(w, `{"status":"ok","agent_connected":true}`)
	} else {
		// Health endpoint stays 200 even without agent — Coolify's healthcheck
		// only cares that the HTTP server is responsive. Whether the agent is
		// connected is a separate operational concern visible in the payload.
		fmt.Fprint(w, `{"status":"ok","agent_connected":false}`)
	}
}

func (s *server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.expectedToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("upgrade failed", "error", err)
		return
	}

	s.agentMu.Lock()
	if s.agent != nil {
		_ = s.agent.Close()
	}
	s.agent = conn
	s.agentMu.Unlock()

	slog.Info("agent connected", "remote", conn.RemoteAddr().String())

	for {
		if _, _, err := conn.NextReader(); err != nil {
			slog.Info("agent disconnected", "error", err)
			s.agentMu.Lock()
			if s.agent == conn {
				s.agent = nil
			}
			s.agentMu.Unlock()
			return
		}
	}
}

func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/files/")

	// Auth check BEFORE peeking the file: avoids leaking existence to
	// unauthenticated probes.
	if r.Header.Get("Authorization") != "Bearer "+s.expectedToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Look up disk path from the in-memory index. The file stays on disk so
	// the background cleanup loop can prune it after the retention window.
	s.mu.Lock()
	entry, ok := s.files[id]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(entry.DiskPath)
	if err != nil {
		slog.Error("open file failed", "path", entry.DiskPath, "error", err)
		http.Error(w, "internal storage error", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, entry.Filename))
	if _, err := io.Copy(w, file); err != nil {
		slog.Warn("stream file failed", "path", entry.DiskPath, "error", err)
	}
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
		return
	}

	client := r.FormValue("client_name")
	if client == "" {
		http.Error(w, "client_name required", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := header.Filename
	if filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	// Reject anything that could escape the data dir or be a Windows-style
	// path. The agent has its own per-segment validator, but a hostile client
	// could still poison our local disk before the file ever reaches the
	// agent — so we rebuild a safe-relative path here too.
	safeRel, err := safeJoinClientPath(client, filename)
	if err != nil {
		http.Error(w, "invalid client/filename: "+err.Error(), http.StatusBadRequest)
		return
	}
	diskPath := filepath.Join(s.dataDir, safeRel)
	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		http.Error(w, "mkdir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out, err := os.Create(diskPath)
	if err != nil {
		http.Error(w, "create file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		_ = os.Remove(diskPath)
		http.Error(w, "write file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(diskPath)
		http.Error(w, "close file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%d", s.nextID)
	s.files[id] = fileEntry{Filename: filename, Client: client, DiskPath: diskPath}
	s.mu.Unlock()

	cmd := map[string]string{
		"action":   "write",
		"client":   client,
		"filename": filename,
		"file_url": "/files/" + id,
	}

	if err := s.sendToAgent(cmd); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	slog.Info("queued write command", "client", client, "filename", filename, "file_id", id)

	storedPath := path.Join("/clientes", client, filename)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"path":%q}`, storedPath)
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	client := r.URL.Query().Get("client_name")
	filename := r.URL.Query().Get("filename")
	if client == "" || filename == "" {
		http.Error(w, "client_name and filename required", http.StatusBadRequest)
		return
	}

	requestID := uuid.NewString()
	ch := make(chan []byte, 1)

	s.mu.Lock()
	s.responses[requestID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.responses, requestID)
		s.mu.Unlock()
	}()

	cmd := map[string]string{
		"action":     "read",
		"client":     client,
		"filename":   filename,
		"upload_url": "/agent/responses/" + requestID,
		"request_id": requestID,
	}

	if err := s.sendToAgent(cmd); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	slog.Info("queued read command", "client", client, "filename", filename, "request_id", requestID)

	select {
	case body := <-ch:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		_, _ = w.Write(body)
	case <-time.After(responseWait):
		http.Error(w, "agent did not respond in time", http.StatusGatewayTimeout)
	case <-r.Context().Done():
		http.Error(w, "client cancelled", http.StatusRequestTimeout)
	}
}

func (s *server) handleAgentResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.expectedToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	requestID := strings.TrimPrefix(r.URL.Path, "/agent/responses/")
	if requestID == "" {
		http.Error(w, "request_id required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	ch, ok := s.responses[requestID]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown request_id", http.StatusNotFound)
		return
	}

	select {
	case ch <- body:
	default:
	}

	w.WriteHeader(http.StatusNoContent)
}

// safeJoinClientPath builds a relative path "<client>/<filename>" rejecting
// inputs that could escape the storage root (path traversal, absolute paths,
// drive letters, backslashes, null bytes). The agent already does a strict
// per-segment validation, but we also enforce it here so a malformed upload
// can't dump files outside the data dir on this side.
func safeJoinClientPath(client, filename string) (string, error) {
	if client == "" || filename == "" {
		return "", fmt.Errorf("client and filename required")
	}
	if strings.ContainsAny(client+filename, "\x00\\") {
		return "", fmt.Errorf("backslash or null byte not allowed")
	}
	// Client can contain forward-slash separators (e.g. "Acme/2026/05/inbox").
	// Validate each segment; reject anything that resolves outside.
	for _, seg := range strings.Split(client, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid client segment: %q", seg)
		}
	}
	if strings.ContainsAny(filename, "/\\") || filename == "." || filename == ".." {
		return "", fmt.Errorf("invalid filename: %q", filename)
	}
	rel := filepath.Join(client, filename)
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("path escapes data dir: %q", clean)
	}
	return clean, nil
}

func (s *server) sendToAgent(cmd map[string]string) error {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.agent == nil {
		return fmt.Errorf("no agent connected")
	}
	if err := s.agent.WriteJSON(cmd); err != nil {
		return fmt.Errorf("send to agent: %w", err)
	}
	return nil
}
