package server

// Config is the runtime configuration for a Server. The caller (typically
// cmd/claudama/main.go) is responsible for loading it from disk and the
// environment. server only consumes a fully-resolved Config.
//
// Fields tagged `toml:"..."` are intended to live in the user's config file.
// Fields tagged `toml:"-"` are populated by the caller from other sources
// (env vars, derived paths) and ignored if present in the file.
type Config struct {
	Port           int    `toml:"port"`
	Debug          bool   `toml:"-"`
	ClaudeModel    string `toml:"-"`
	ConfigFilePath string `toml:"-"` // canonical path of the config file; used only in error messages
}

// DefaultConfig returns the baseline Config. Callers should start from this
// and overlay any user-provided overrides.
func DefaultConfig() Config {
	return Config{Port: 11434}
}
