package server

import (
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name       string
		messages   []ollamaMessage
		wantPrompt string
		wantSystem string
	}{
		{
			name:       "lone user message is verbatim",
			messages:   []ollamaMessage{{Role: "user", Content: "hello"}},
			wantPrompt: "hello",
			wantSystem: "",
		},
		{
			name: "system messages concatenate and stay out of prompt",
			messages: []ollamaMessage{
				{Role: "system", Content: "be terse"},
				{Role: "system", Content: "no emoji"},
				{Role: "user", Content: "hi"},
			},
			wantPrompt: "hi",
			wantSystem: "be terse\n\nno emoji",
		},
		{
			name: "multi-turn ending in user gets Assistant nudge",
			messages: []ollamaMessage{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "reply"},
				{Role: "user", Content: "second"},
			},
			wantPrompt: "User: first\n\nAssistant: reply\n\nUser: second\n\nAssistant:",
			wantSystem: "",
		},
		{
			name: "multi-turn ending in assistant has no nudge",
			messages: []ollamaMessage{
				{Role: "user", Content: "q"},
				{Role: "assistant", Content: "a"},
			},
			wantPrompt: "User: q\n\nAssistant: a",
			wantSystem: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPrompt, gotSystem := buildPrompt(tc.messages)
			if gotPrompt != tc.wantPrompt {
				t.Errorf("prompt:\n  got:  %q\n  want: %q", gotPrompt, tc.wantPrompt)
			}
			if gotSystem != tc.wantSystem {
				t.Errorf("system:\n  got:  %q\n  want: %q", gotSystem, tc.wantSystem)
			}
		})
	}
}

func TestScanClaudeStream(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hel"}}}`,
		`not valid json — should be skipped, not fatal`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"result","usage":{"input_tokens":12,"output_tokens":34}}`,
	}, "\n")

	var got strings.Builder
	res, err := scanClaudeStream(strings.NewReader(stream), func(delta string) error {
		got.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("scanClaudeStream returned error: %v", err)
	}
	if got.String() != "hello" {
		t.Errorf("deltas: got %q, want %q", got.String(), "hello")
	}
	if res.InputTokens != 12 || res.OutputTokens != 34 {
		t.Errorf("usage: got %+v, want {12 34}", res)
	}
}
