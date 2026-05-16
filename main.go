package main

import (
	"bytes"
	"cmp"
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

type config struct {
	Addr        string
	Debug       bool
	ClaudePath  string
	ClaudeModel string // optional default fallback when no per-request model is set
}

func loadConfig() (config, error) {
	cfg := config{
		Addr:        cmp.Or(os.Getenv("ADDR"), "127.0.0.1:11434"),
		Debug:       os.Getenv("CLAUDAMA_DEBUG") != "",
		ClaudeModel: os.Getenv("CLAUDE_MODEL"),
	}
	p, err := exec.LookPath("claude")
	if err != nil {
		return cfg, fmt.Errorf("claude CLI not found in PATH: %w", err)
	}
	cfg.ClaudePath = p
	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tags", handleTags)
	mux.HandleFunc("POST /api/show", handleShow)
	mux.HandleFunc("POST /api/chat", handleChat(cfg))

	srv := &http.Server{
		Handler:           logRequests(cfg, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		reportBindError(cfg.Addr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		slog.Info("claudama listening", "addr", cfg.Addr, "claude", cfg.ClaudePath)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err, ok := <-errc:
		if ok {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}

// reportBindError prints a human-readable message when claudama can't bind its
// port. The common case on a fresh install is that Ollama itself is already
// listening on 11434 — probe and say so explicitly.
func reportBindError(addr string, err error) {
	if !errors.Is(err, syscall.EADDRINUSE) {
		fmt.Fprintf(os.Stderr, "claudama: failed to listen on %s: %v\n", addr, err)
		return
	}
	occupant := "another process"
	if isOllama(addr) {
		occupant = "Ollama"
	}
	fmt.Fprintf(os.Stderr, `claudama: port %s is already in use by %s.

claudama defaults to Ollama's port (11434) so Ollama-compatible clients
(e.g. Raycast) find it without configuration. Pick one:

  • Stop Ollama, then start claudama:
      brew services stop ollama   # or: pkill ollama
      claudama

  • Or run claudama on a different port:
      ADDR=127.0.0.1:11436 claudama
    (then point your client at http://127.0.0.1:11436)
`, addr, occupant)
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

func logRequests(cfg config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attrs := []any{"method", r.Method, "path", r.URL.Path}
		if cfg.Debug && r.Body != nil && r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			attrs = append(attrs, "body", string(body))
		}
		slog.Info("request", attrs...)
		next.ServeHTTP(w, r)
	})
}
