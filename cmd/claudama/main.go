package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/atelpis/claudama/internal/server"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("startup", "err", err)
		os.Exit(1)
	}
	if err := srv.Run(); err != nil {
		os.Exit(1)
	}
}

// loadConfig builds a server.Config by overlaying ~/.config/claudama/conf.toml
// (when present) on top of server.DefaultConfig(), then layering env-driven
// fields on top.
func loadConfig() (server.Config, error) {
	cfg := server.DefaultConfig()

	path, err := configPath()
	if err != nil {
		return cfg, err
	}
	cfg.ConfigFilePath = path

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// no file → defaults are fine
	case err != nil:
		return cfg, fmt.Errorf("read %s: %w", path, err)
	default:
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("%s: invalid port %d (must be 1–65535)", path, cfg.Port)
	}

	cfg.Debug = os.Getenv("CLAUDAMA_DEBUG") != ""
	cfg.ClaudeModel = os.Getenv("CLAUDE_MODEL")
	return cfg, nil
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claudama", "conf.toml"), nil
}
