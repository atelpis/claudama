package server

const fakeModelDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// modelEntry maps an Ollama-facing tag (what clients see in `/api/tags`) to
// the `--model` argument passed to the `claude` CLI.
type modelEntry struct {
	Tag           string // e.g. "claudama-opus:4.7"
	ClaudeArg     string // e.g. "claude-opus-4-7"
	ParameterSize string // displayed parameter size (Raycast shows this)
}

// models is the list exposed via /api/tags, in display order. The first entry
// is the default for unknown/empty `model` fields in chat requests.
var models = []modelEntry{
	{Tag: "claudama-sonnet:4.6", ClaudeArg: "claude-sonnet-4-6", ParameterSize: "200B"},
	{Tag: "claudama-opus:4.7", ClaudeArg: "claude-opus-4-7", ParameterSize: "400B"},
	{Tag: "claudama-haiku:4.5", ClaudeArg: "claude-haiku-4-5-20251001", ParameterSize: "70B"},
}

// resolveModel returns the entry for a given Ollama tag, or the default when
// the tag is unknown or empty.
func resolveModel(tag string) modelEntry {
	for _, m := range models {
		if m.Tag == tag {
			return m
		}
	}
	return models[0]
}
