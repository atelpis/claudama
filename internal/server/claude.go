package server

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// claudeStreamEvent matches the subset of `claude -p --output-format stream-json
// --include-partial-messages` output we care about. Unknown fields are ignored.
type claudeStreamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Event   struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Result string `json:"result"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type claudeResult struct {
	InputTokens  int
	OutputTokens int
}

// streamClaude shells out to the local `claude` CLI, streams text deltas to
// onDelta, and returns final usage info from the result event.
func (s *Server) streamClaude(ctx context.Context, claudeModel string, messages []ollamaMessage, onDelta func(string) error) (claudeResult, error) {
	prompt, system := buildPrompt(messages)
	if prompt == "" {
		return claudeResult{}, fmt.Errorf("no user message in request")
	}

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--no-session-persistence",
		"--tools", "", // pure chat — no Read/Bash/etc.
	}
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	if model := cmp.Or(claudeModel, s.cfg.ClaudeModel); model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, s.claudePath, args...)
	// Pin CWD to a TCC-neutral path. Claude scans the working directory at
	// startup for CLAUDE.md / git context; if claudama was launched from a
	// location under ~ that traversal hits ~/Music, ~/Pictures, etc. and macOS
	// prompts for photo/music access against the parent (claudama).
	cmd.Dir = os.TempDir()
	// Drop the CLAUDECODE guard so the server can run from inside a Claude Code
	// session during development. Augment PATH so claude's npm shim can find
	// `node` under launchd (`brew services`), where inherited PATH is minimal.
	env := filterEnv(os.Environ(), "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")
	cmd.Env = augmentPath(env, s.claudePath)
	if s.cfg.Debug {
		cmd.Stderr = os.Stderr
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return claudeResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return claudeResult{}, err
	}

	result, scanErr := scanClaudeStream(stdout, onDelta)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return result, scanErr
	}
	if waitErr != nil {
		return result, fmt.Errorf("claude exited: %w", waitErr)
	}
	return result, nil
}

func scanClaudeStream(r io.Reader, onDelta func(string) error) (claudeResult, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var result claudeResult
	for scanner.Scan() {
		var evt claudeStreamEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "stream_event":
			if evt.Event.Type == "content_block_delta" && evt.Event.Delta.Type == "text_delta" && evt.Event.Delta.Text != "" {
				if err := onDelta(evt.Event.Delta.Text); err != nil {
					return result, err
				}
			}
		case "result":
			result.InputTokens = evt.Usage.InputTokens
			result.OutputTokens = evt.Usage.OutputTokens
		}
	}
	return result, scanner.Err()
}

// buildPrompt converts Ollama-style messages into a single prompt string and a
// system prompt. A lone user message is passed verbatim; multi-turn histories
// are formatted with role labels so claude sees the conversation in order.
func buildPrompt(messages []ollamaMessage) (prompt, system string) {
	var sys []string
	var convo []ollamaMessage
	for _, m := range messages {
		if m.Role == "system" {
			sys = append(sys, m.Content)
			continue
		}
		convo = append(convo, m)
	}
	system = strings.Join(sys, "\n\n")

	if len(convo) == 1 && convo[0].Role == "user" {
		return convo[0].Content, system
	}

	var sb strings.Builder
	for i, m := range convo {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch m.Role {
		case "user":
			sb.WriteString("User: ")
		case "assistant":
			sb.WriteString("Assistant: ")
		default:
			sb.WriteString(m.Role)
			sb.WriteString(": ")
		}
		sb.WriteString(m.Content)
	}
	// Nudge claude to produce the next assistant turn.
	if len(convo) > 0 && convo[len(convo)-1].Role == "user" {
		sb.WriteString("\n\nAssistant:")
	}
	return sb.String(), system
}

func filterEnv(env []string, drop ...string) []string {
	dropSet := make(map[string]struct{}, len(drop))
	for _, k := range drop {
		dropSet[k] = struct{}{}
	}
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, skip := dropSet[k]; skip {
			continue
		}
		out = append(out, kv)
	}
	return out
}
