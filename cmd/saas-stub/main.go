package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const responseWait = 30 * time.Second

type fileEntry struct {
	Filename string
	Client   string
	Content  []byte
}

type server struct {
	mu            sync.Mutex
	files         map[string]fileEntry
	responses     map[string]chan []byte
	nextID        int
	agent         *websocket.Conn
	agentMu       sync.Mutex
	expectedToken string
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

	s := &server{
		files:         make(map[string]fileEntry),
		responses:     make(map[string]chan []byte),
		expectedToken: resolvedToken,
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
	slog.Info("saas-stub listening", "addr", resolvedAddr, "token_source", tokenSource)
	if err := http.ListenAndServe(resolvedAddr, mux); err != nil {
		slog.Error("server failed", "error", err)
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

	// Delete-after-fetch: keeps the in-memory queue empty as soon as the agent
	// picks up the file. Satisfies the operational rule of never persisting
	// client data on our server — file lives in RAM only for the few seconds
	// between Laravel's POST and the agent's GET.
	s.mu.Lock()
	entry, ok := s.files[id]
	if ok {
		delete(s.files, id)
	}
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, entry.Filename))
	_, _ = w.Write(entry.Content)
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

	body, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read file: "+err.Error(), http.StatusBadRequest)
		return
	}

	filename := header.Filename
	if filename == "" {
		http.Error(w, "filename required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%d", s.nextID)
	s.files[id] = fileEntry{Filename: filename, Client: client, Content: body}
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
