package server

import (
	"bytes"
	"context"
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
// bind. claudama defaults to 11434 (Ollama's port), so a collision is likely
// — but we don't probe; we just point at the two ways out.
func (s *Server) reportBindError(addr string, err error) {
	if !errors.Is(err, syscall.EADDRINUSE) {
		fmt.Fprintf(os.Stderr, "claudama: failed to listen on %s: %v\n", addr, err)
		return
	}
	cfgPath := s.cfg.ConfigFilePath
	if cfgPath == "" {
		cfgPath = "~/.config/claudama/conf.toml"
	}
	fmt.Fprintf(os.Stderr, `claudama: %s is already in use (likely Ollama).

Either free the port, or change claudama's port in %s:
  port = 11435
`, addr, cfgPath)
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
