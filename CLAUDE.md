# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Purpose

claudama is an Ollama-API-compatible HTTP server that forwards chat requests to the local `claude` CLI (Claude Code). It exists so tools that speak Ollama (e.g. Raycast) can use a Claude Code subscription instead of a per-token Anthropic API key. Design decisions should preserve that goal — do not add Anthropic-API/token paths.

## Commands

- Build: `go build ./...`
- Vet: `go vet ./...`
- Run: `go run ./cmd/claudama` (listens on `127.0.0.1:11434` — Ollama's default port). To change the port, edit (or create, if missing) `~/.config/claudama/conf.toml` and set `port = 11436`. Lookup order: `~/.config/claudama/conf.toml` first; if missing, the well-known system paths (`/opt/homebrew/etc/claudama/conf.toml`, then `/usr/local/etc/claudama/conf.toml`) are probed in order. The Homebrew formula seeds its etc copy on install so brew users get a working file out of the box without anything in $HOME. The probe list lives in `wellKnownEtcConfigPaths` in `cmd/claudama/main.go` — probing at runtime (rather than baking in `etcConfigDir` via ldflags) lets the same goreleaser-built binary work under either brew prefix or a non-brew install. There is no `ADDR` env var — config is file-only so installs have one canonical place to edit. The file overrides only the fields it specifies; `server.DefaultConfig()` supplies the rest.
- Version: `claudama -version` prints the build version. `var version` in `main.go` defaults to `"dev"` and is overridden by `-ldflags "-X main.version=v0.1.0"` at release/brew build time.
- Install locally: `go install ./cmd/claudama` (produces `$(go env GOBIN)/claudama` — same binary Homebrew will ship).
- Select default Claude model: `CLAUDE_MODEL=claude-opus-4-7 go run ./cmd/claudama`
- Test: `go test ./...`
- Smoke test the chat endpoint:
  ```
  curl -sN -X POST http://127.0.0.1:11434/api/chat \
    -H 'Content-Type: application/json' \
    -d '{"model":"claudama-sonnet:4.6","messages":[{"role":"user","content":"say pong"}]}'
  ```

## Architecture

Standard Go layout: the binary lives at `cmd/claudama/` (so `go install ./cmd/claudama` ships a single `claudama` executable — matters for Homebrew). All source files are in `package main` inside that directory; `go.mod` stays at the repo root. There is no library package — claudama is not meant to be imported.

Files under `cmd/claudama/`:

- `main.go` — HTTP bootstrap and config. Registers `GET /api/tags` (model list), `POST /api/show` (per-model details + capabilities — Raycast calls this after `/api/tags` and marks the model unavailable if it 404s), and `POST /api/chat`. Port comes from `~/.config/claudama/conf.toml` (`port = N`), defaulting to `11434` when the file is absent. `defaultFileConfig()` is the single source of truth for defaults — `loadFileConfig` starts from that struct and lets `toml.Unmarshal` patch in whatever the user specified, so partial files are fine. Host is always `127.0.0.1`. When the port is busy, `reportBindError` probes `/api/version` to detect whether Ollama itself is the occupant and prints a tailored message pointing at the config file.
- `ollama.go` — Ollama wire format. `handleChat` decodes the Ollama `chatRequest`, calls `streamClaude`, and emits responses. Two modes:
  - **Streaming (default):** NDJSON chunks, one per text delta, terminated by a `done:true` chunk with usage stats.
  - **Non-streaming (`"stream": false`):** buffers all deltas and returns a single JSON object.
  - On backend error mid-stream, a final `done:true` chunk with `done_reason:"error"` is emitted so clients don't hang.
- `claude.go` — Subprocess bridge. `streamClaude` shells out to:
  ```
  claude -p <prompt> --output-format stream-json --verbose \
         --include-partial-messages --no-session-persistence --tools ""
  ```
  Parses NDJSON events, forwards `content_block_delta`/`text_delta` text to the caller's `onDelta`, and captures `input_tokens`/`output_tokens` from the terminal `result` event.

### Message → prompt mapping (`buildPrompt`)
- `system` messages are concatenated and passed via `--append-system-prompt`.
- A single user message is sent verbatim as the prompt.
- Multi-turn histories are flattened with `User:` / `Assistant:` labels and a trailing `\n\nAssistant:` nudge.

### Env scrubbing
`streamClaude` strips `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` from the child env so the server can run from inside a Claude Code session during development (without that, nested-session guard rails reject the spawn).

### Tooling disabled
`--tools ""` is intentional — claudama is pure chat. Do not add Read/Bash/etc. without a clear reason; the upstream client (e.g. Raycast) expects a plain LLM.

## Model registry
`models.go` defines the tags exposed via `/api/tags`: `claudama-sonnet:4.6`, `claudama-opus:4.7`, `claudama-haiku:4.5`. `resolveModel` maps a client-supplied tag to its `claude --model` argument, falling back to the first entry on unknown/empty tags. `CLAUDE_MODEL` env var is a secondary fallback used only when the per-request tag does not match a registered entry.
