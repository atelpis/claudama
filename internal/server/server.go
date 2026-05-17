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
	"path/filepath"
	"strings"
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
	p, err := resolveClaude(cfg.ClaudePath)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, claudePath: p}, nil
}

// resolveClaude returns an absolute path to the `claude` binary. Resolution
// order: explicit claude_path → $PATH → well-known install locations. The
// fallback list exists because launchd (and therefore `brew services`) gives
// child processes a minimal PATH that excludes /opt/homebrew/bin etc., so
// LookPath fails even when claude is installed in an obvious place.
func resolveClaude(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("claude_path %q: %w", configured, err)
		}
		return configured, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	if p, ok := findClaudeInWellKnownPaths(); ok {
		return p, nil
	}
	return "", fmt.Errorf("claude CLI not found in PATH or any well-known location (set claude_path in conf.toml to override)")
}

// wellKnownClaudePaths is the list of locations resolveClaude probes when $PATH
// lookup fails. Entries beginning with "~/" are expanded against the current
// user's home directory at lookup time. The same directories also seed the
// PATH passed to spawned `claude` processes (see augmentPath) so the node
// shebang inside the npm-installed claude shim can find `node`.
var wellKnownClaudePaths = []string{
	"/opt/homebrew/bin/claude", // Apple Silicon Homebrew, Anthropic .pkg
	"/usr/local/bin/claude",    // Intel Homebrew, default npm prefix
	"~/.local/bin/claude",      // pipx / user-prefix npm
	"~/.npm-global/bin/claude", // common custom npm prefix
	"~/.bun/bin/claude",        // bun global install
	"/usr/bin/claude",          // distro package managers (linux)
}

func findClaudeInWellKnownPaths() (string, bool) {
	home, _ := os.UserHomeDir()
	for _, p := range wellKnownClaudePaths {
		if home != "" && len(p) >= 2 && p[:2] == "~/" {
			p = filepath.Join(home, p[2:])
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// augmentPath returns env with PATH extended to include the directory of the
// resolved claude binary plus every wellKnownClaudePaths parent directory.
// This matters under `brew services` (and any other launchd-spawned scenario)
// where the inherited PATH is stripped to /usr/bin:/bin:/usr/sbin:/sbin —
// claude's `#!/usr/bin/env node` shebang would otherwise fail with exit 127
// even though claudama itself resolved claude correctly.
func augmentPath(env []string, claudePath string) []string {
	home, _ := os.UserHomeDir()
	extras := []string{filepath.Dir(claudePath)}
	for _, p := range wellKnownClaudePaths {
		if home != "" && len(p) >= 2 && p[:2] == "~/" {
			p = filepath.Join(home, p[2:])
		}
		extras = append(extras, filepath.Dir(p))
	}

	for i, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == "PATH" {
			env[i] = "PATH=" + v + ":" + strings.Join(extras, ":")
			return env
		}
	}
	return append(env, "PATH="+strings.Join(extras, ":"))
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
