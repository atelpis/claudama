package main

import "testing"

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want string
	}{
		{"known tag", "claudama-opus:4.7", "claude-opus-4-7"},
		{"unknown tag falls back to first entry", "bogus", models[0].ClaudeArg},
		{"empty tag falls back to first entry", "", models[0].ClaudeArg},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveModel(tc.tag).ClaudeArg
			if got != tc.want {
				t.Errorf("resolveModel(%q).ClaudeArg = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}
}
