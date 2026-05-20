package saas

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gustavoguarda/pier/internal/config"
	"github.com/gustavoguarda/pier/internal/filestore"
)

const (
	initialBackoff   = time.Second
	maxBackoff       = 60 * time.Second
	stableConnection = 30 * time.Second
)

type Command struct {
	Action    string `json:"action"`
	Client    string `json:"client"`
	Filename  string `json:"filename"`
	FileURL   string `json:"file_url"`
	UploadURL string `json:"upload_url"`
	RequestID string `json:"request_id"`
}

func Run(ctx context.Context, cfg *config.Config, store *filestore.Store) error {
	backoff := initialBackoff
	for {
		connectedAt := time.Now()
		err := connectAndServe(ctx, cfg, store)
		if ctx.Err() != nil {
			return nil
		}

		if time.Since(connectedAt) > stableConnection {
			backoff = initialBackoff
		}

		slog.Warn("connection lost", "error", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func connectAndServe(ctx context.Context, cfg *config.Config, store *filestore.Store) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Token)

	slog.Info("connecting to SaaS", "url", cfg.SaasURL)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, cfg.SaasURL, header)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	slog.Info("connected to SaaS")

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for {
		var cmd Command
		if err := conn.ReadJSON(&cmd); err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		go func(cmd Command) {
			if err := handleCommand(ctx, cfg, store, cmd); err != nil {
				slog.Error("command failed",
					"action", cmd.Action,
					"client", cmd.Client,
					"filename", cmd.Filename,
					"request_id", cmd.RequestID,
					"error", err)
			}
		}(cmd)
	}
}

func handleCommand(ctx context.Context, cfg *config.Config, store *filestore.Store, cmd Command) error {
	switch cmd.Action {
	case "write":
		return handleWrite(ctx, cfg, store, cmd)
	case "read":
		return handleRead(ctx, cfg, store, cmd)
	default:
		return fmt.Errorf("unknown action: %s", cmd.Action)
	}
}

func handleWrite(ctx context.Context, cfg *config.Config, store *filestore.Store, cmd Command) error {
	fileURL, err := resolveURL(cfg.HTTPBaseURL, cmd.FileURL)
	if err != nil {
		return fmt.Errorf("resolve file_url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fetch file: status %d, body: %s", resp.StatusCode, string(body))
	}

	path, err := store.Write(cmd.Client, cmd.Filename, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	slog.Info("wrote file", "path", path, "client", cmd.Client, "filename", cmd.Filename)
	return nil
}

func handleRead(ctx context.Context, cfg *config.Config, store *filestore.Store, cmd Command) error {
	if cmd.UploadURL == "" {
		return fmt.Errorf("upload_url is required for read")
	}

	rc, err := store.Read(cmd.Client, cmd.Filename)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}
	defer rc.Close()

	uploadURL, err := resolveURL(cfg.HTTPBaseURL, cmd.UploadURL)
	if err != nil {
		return fmt.Errorf("resolve upload_url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, rc)
	if err != nil {
		return fmt.Errorf("build response request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post response: status %d, body: %s", resp.StatusCode, string(body))
	}

	slog.Info("served file", "client", cmd.Client, "filename", cmd.Filename, "request_id", cmd.RequestID)
	return nil
}

// resolveURL turns a SaaS-supplied URL reference into a fully qualified URL,
// rejecting any absolute reference whose scheme+host does not match the
// configured http_base_url. This prevents a compromised SaaS from steering
// the agent to attacker-controlled hosts.
func resolveURL(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	if baseURL.Host == "" || baseURL.Scheme == "" {
		return "", fmt.Errorf("base url must include scheme and host: %q", base)
	}

	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		refURL, err := url.Parse(ref)
		if err != nil {
			return "", fmt.Errorf("parse ref url: %w", err)
		}
		if refURL.Scheme != baseURL.Scheme || refURL.Host != baseURL.Host {
			return "", fmt.Errorf("host %s://%s not allowed (expected %s://%s)",
				refURL.Scheme, refURL.Host, baseURL.Scheme, baseURL.Host)
		}
		return ref, nil
	}

	if strings.Contains(ref, "://") {
		return "", fmt.Errorf("unsupported scheme in ref: %q", ref)
	}

	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(ref, "/"), nil
}
