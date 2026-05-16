package main

import (
	"cmp"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type tagsResponse struct {
	Models []tagModel `json:"models"`
}

type tagModel struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    tagModelInfo `json:"details"`
}

type tagModelInfo struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type showRequest struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

type showResponse struct {
	License      string         `json:"license,omitempty"`
	Modelfile    string         `json:"modelfile"`
	Parameters   string         `json:"parameters"`
	Template     string         `json:"template"`
	Details      tagModelInfo   `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   *bool           `json:"stream,omitempty"`
}

type chatResponse struct {
	Model      string        `json:"model"`
	CreatedAt  time.Time     `json:"created_at"`
	Message    ollamaMessage `json:"message"`
	Done       bool          `json:"done"`
	DoneReason string        `json:"done_reason,omitempty"`

	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

func modelDetails(m modelEntry) tagModelInfo {
	return tagModelInfo{
		Format:            "gguf",
		Family:            "llama",
		Families:          []string{"llama"},
		ParameterSize:     m.ParameterSize,
		QuantizationLevel: "Q4_K_M",
	}
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	resp := tagsResponse{Models: make([]tagModel, 0, len(models))}
	now := time.Now().UTC()
	for _, m := range models {
		resp.Models = append(resp.Models, tagModel{
			Name:       m.Tag,
			Model:      m.Tag,
			ModifiedAt: now,
			Size:       4000000000,
			Digest:     fakeModelDigest,
			Details:    modelDetails(m),
		})
	}
	writeJSON(w, resp)
}

func handleShow(w http.ResponseWriter, r *http.Request) {
	var req showRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	m := resolveModel(cmp.Or(req.Model, req.Name))

	writeJSON(w, showResponse{
		Modelfile:  "# claudama — forwards to local `claude` CLI (" + m.ClaudeArg + ")\n",
		Parameters: "",
		Template:   "{{ .Prompt }}",
		Details:    modelDetails(m),
		ModelInfo: map[string]any{
			"general.architecture":    "llama",
			"general.parameter_count": 8000000000,
			"llama.context_length":    200000,
		},
		Capabilities: []string{"completion"},
	})
}

func handleChat(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m := resolveModel(req.Model)
		tag := cmp.Or(req.Model, m.Tag)
		stream := req.Stream == nil || *req.Stream
		logChat(req, m, stream)

		start := time.Now()
		if stream {
			writeStreamingChat(w, r, cfg, tag, m, req.Messages, start)
			return
		}
		writeBufferedChat(w, r, cfg, tag, m, req.Messages, start)
	}
}

func logChat(req chatRequest, m modelEntry, stream bool) {
	var sys, user, asst, totalChars int
	for _, msg := range req.Messages {
		totalChars += len(msg.Content)
		switch msg.Role {
		case "system":
			sys++
		case "user":
			user++
		case "assistant":
			asst++
		}
	}
	slog.Info("chat",
		"requested", req.Model,
		"claude", m.ClaudeArg,
		"stream", stream,
		"messages", len(req.Messages),
		"sys", sys, "user", user, "asst", asst,
		"chars", totalChars,
	)
}

func writeBufferedChat(w http.ResponseWriter, r *http.Request, cfg config, tag string, m modelEntry, messages []ollamaMessage, start time.Time) {
	var sb strings.Builder
	res, err := streamClaude(r.Context(), cfg, m.ClaudeArg, messages, func(delta string) error {
		sb.WriteString(delta)
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, terminalChunk(tag, sb.String(), "stop", start, res))
}

func writeStreamingChat(w http.ResponseWriter, r *http.Request, cfg config, tag string, m modelEntry, messages []ollamaMessage, start time.Time) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)

	res, err := streamClaude(r.Context(), cfg, m.ClaudeArg, messages, func(delta string) error {
		if err := enc.Encode(chatResponse{
			Model:     tag,
			CreatedAt: time.Now().UTC(),
			Message:   ollamaMessage{Role: "assistant", Content: delta},
			Done:      false,
		}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		// Best-effort: surface the error in a final chunk so the client doesn't hang.
		_ = enc.Encode(chatResponse{
			Model:      tag,
			CreatedAt:  time.Now().UTC(),
			Message:    ollamaMessage{Role: "assistant", Content: "\n\n[claudama error: " + err.Error() + "]"},
			Done:       true,
			DoneReason: "error",
		})
		flusher.Flush()
		return
	}
	_ = enc.Encode(terminalChunk(tag, "", "stop", start, res))
	flusher.Flush()
}

func terminalChunk(tag, content, reason string, start time.Time, res claudeResult) chatResponse {
	return chatResponse{
		Model:           tag,
		CreatedAt:       time.Now().UTC(),
		Message:         ollamaMessage{Role: "assistant", Content: content},
		Done:            true,
		DoneReason:      reason,
		TotalDuration:   time.Since(start).Nanoseconds(),
		PromptEvalCount: res.InputTokens,
		EvalCount:       res.OutputTokens,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode", "err", err)
	}
}
