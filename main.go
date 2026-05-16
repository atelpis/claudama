package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
		Addr:        cmp.Or(os.Getenv("ADDR"), "127.0.0.1:11436"),
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
		Addr:              cfg.Addr,
		Handler:           logRequests(cfg, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		slog.Info("claudama listening", "addr", cfg.Addr, "claude", cfg.ClaudePath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
