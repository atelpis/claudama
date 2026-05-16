# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Purpose

claudama is an Ollama-API-compatible HTTP server that forwards chat requests to the local `claude` CLI (Claude Code). It exists so tools that speak Ollama (e.g. Raycast) can use a Claude Code subscription instead of a per-token Anthropic API key. Design decisions should preserve that goal — do not add Anthropic-API/token paths.

## Commands

- Build: `go build ./...`
- Vet: `go vet ./...`
- Run: `go run .` (listens on `127.0.0.1:11436`; override with `ADDR=host:port`)
- Select Claude model: `CLAUDE_MODEL=claude-opus-4-7 go run .`
- Smoke test the chat endpoint:
  ```
  curl -sN -X POST http://127.0.0.1:11436/api/chat \
    -H 'Content-Type: application/json' \
    -d '{"model":"claudama:latest","messages":[{"role":"user","content":"say pong"}]}'
  ```

No test suite yet.

## Architecture

Three files, one `main` package:

- `main.go` — HTTP bootstrap. Registers `GET /api/tags` (model list), `POST /api/show` (per-model details + capabilities — Raycast calls this after `/api/tags` and marks the model unavailable if it 404s), and `POST /api/chat`. Default listen address `127.0.0.1:11436` (not Ollama's default 11434 — clients must be pointed explicitly).
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

## Model name
`/api/chat` accepts any `model` string from the client and echoes it back in responses. The Ollama-side identity is `claudama:latest` (constant `fakeModelName`). The actual Claude model is selected via the `CLAUDE_MODEL` env var passed to the `claude` CLI, independent of what the client sends.
