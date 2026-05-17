package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/atelpis/claudama/internal/server"
)

// version is overridden at build time via -ldflags "-X main.version=v0.1.0".
// Homebrew (and the Makefile) inject the real value; `go run` / `go install`
// without ldflags leaves it as "dev".
var version = "dev"

// etcConfigDir is overridden at build time via
// -ldflags "-X main.etcConfigDir=/opt/homebrew/etc/claudama" so the Homebrew
// formula can point the binary at its system-wide config seed. Empty when
// unset → the etc fallback is simply skipped (source builds keep the
// ~/.config-only behavior).
var etcConfigDir = ""

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

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

// loadConfig builds a server.Config by overlaying a TOML config file (when
// present) on top of server.DefaultConfig(), then layering env-driven fields
// on top. Lookup order: ~/.config/claudama/conf.toml first; if missing and the
// binary was built with an etcConfigDir (Homebrew), that directory's conf.toml
// is the fallback.
func loadConfig() (server.Config, error) {
	cfg := server.DefaultConfig()

	path, err := resolveConfigPath()
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

// resolveConfigPath returns the first existing config file path, falling back
// to the user-scoped path when nothing exists (so callers always have a stable
// path to surface in errors / `/api/show`). The user path always wins when
// present; the etc path is only consulted on Homebrew-built binaries where
// etcConfigDir was injected at link time.
func resolveConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	userPath := filepath.Join(home, ".config", "claudama", "conf.toml")

	if _, err := os.Stat(userPath); err == nil {
		return userPath, nil
	}
	if etcConfigDir != "" {
		etcPath := filepath.Join(etcConfigDir, "conf.toml")
		if _, err := os.Stat(etcPath); err == nil {
			return etcPath, nil
		}
	}
	return userPath, nil
}
