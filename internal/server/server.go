package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// Server wraps the resolved Config plus runtime-derived state (the discovered
// claude binary path). It owns the HTTP server lifecycle.
type Server struct {
	cfg        Config
	claudePath string
}

// New validates the environment (resolves the claude CLI) and returns a
// Server ready to Run. It does not start the HTTP listener.
func New(cfg Config) (*Server, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found in PATH: %w", err)
	}
	return &Server{cfg: cfg, claudePath: p}, nil
}

func (s *Server) addr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
}

// Run starts the HTTP server and blocks until SIGINT/SIGTERM or an
// unrecoverable error. All user-visible output (slog logs, bind-error help
// text) is handled here — the caller only needs to translate a non-nil
// error into a non-zero exit code.
func (s *Server) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tags", handleTags)
	mux.HandleFunc("POST /api/show", handleShow)
	mux.HandleFunc("POST /api/chat", s.handleChat())

	srv := &http.Server{
		Handler:           s.logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	addr := s.addr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.reportBindError(addr, err)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		slog.Info("claudama listening", "addr", addr, "claude", s.claudePath)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	var runErr error
	select {
	case err, ok := <-errc:
		if ok {
			slog.Error("server failed", "err", err)
			runErr = err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	return runErr
}

// reportBindError prints a human-readable message when the listener can't
// bind. The common case on a fresh install is that Ollama itself is already
// listening on 11434 — probe and say so explicitly.
func (s *Server) reportBindError(addr string, err error) {
	if !errors.Is(err, syscall.EADDRINUSE) {
		fmt.Fprintf(os.Stderr, "claudama: failed to listen on %s: %v\n", addr, err)
		return
	}
	occupant := "another process"
	if isOllama(addr) {
		occupant = "Ollama"
	}
	cfgPath := s.cfg.ConfigFilePath
	if cfgPath == "" {
		cfgPath = "~/.config/claudama/conf.toml"
	}
	fmt.Fprintf(os.Stderr, `claudama: %s is already in use by %s.

claudama defaults to Ollama's port (11434) so Ollama-compatible clients
(e.g. Raycast) find it without configuration. Pick one:

  • Stop Ollama, then start claudama:
      brew services stop ollama   # or: pkill ollama
      claudama

  • Or run claudama on a different port by editing (or creating) %s
    and setting:
      port = 11436
    Then point your client at http://127.0.0.1:11436
`, addr, occupant, cfgPath)
}

// isOllama returns true when the process listening on addr looks like Ollama
// (its /api/version endpoint returns a JSON object with a "version" field).
func isOllama(addr string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/api/version")
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return body.Version != ""
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attrs := []any{"method", r.Method, "path", r.URL.Path}
		if s.cfg.Debug && r.Body != nil && r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			attrs = append(attrs, "body", string(body))
		}
		slog.Info("request", attrs...)
		next.ServeHTTP(w, r)
	})
}
