package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	fakeModelName   = "claudama:latest"
	fakeModelDigest = "0000000000000000000000000000000000000000000000000000000000000000"
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

func handleShow(w http.ResponseWriter, r *http.Request) {
	// Body is ignored — we only ever serve one model.
	var req showRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	writeJSON(w, showResponse{
		Modelfile:  "# claudama — forwards to local `claude` CLI\n",
		Parameters: "",
		Template:   "{{ .Prompt }}",
		Details: tagModelInfo{
			Format:            "gguf",
			Family:            "llama",
			Families:          []string{"llama"},
			ParameterSize:     "8B",
			QuantizationLevel: "Q4_K_M",
		},
		ModelInfo: map[string]any{
			"general.architecture":    "llama",
			"general.parameter_count": 8000000000,
			"llama.context_length":    200000,
		},
		Capabilities: []string{"completion"},
	})
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, tagsResponse{
		Models: []tagModel{{
			Name:       fakeModelName,
			Model:      fakeModelName,
			ModifiedAt: time.Now().UTC(),
			Size:       4000000000,
			Digest:     fakeModelDigest,
			Details: tagModelInfo{
				Format:            "gguf",
				Family:            "llama",
				Families:          []string{"llama"},
				ParameterSize:     "8B",
				QuantizationLevel: "Q4_K_M",
			},
		}},
	})
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

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	model := req.Model
	if model == "" {
		model = fakeModelName
	}
	stream := req.Stream == nil || *req.Stream
	start := time.Now()

	if !stream {
		var sb strings.Builder
		res, err := streamClaude(r.Context(), req.Messages, func(delta string) error {
			sb.WriteString(delta)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, chatResponse{
			Model:           model,
			CreatedAt:       time.Now().UTC(),
			Message:         ollamaMessage{Role: "assistant", Content: sb.String()},
			Done:            true,
			DoneReason:      "stop",
			TotalDuration:   time.Since(start).Nanoseconds(),
			PromptEvalCount: res.InputTokens,
			EvalCount:       res.OutputTokens,
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)

	res, err := streamClaude(r.Context(), req.Messages, func(delta string) error {
		if err := enc.Encode(chatResponse{
			Model:     model,
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
		// Best-effort: surface the error in a final chunk so Raycast doesn't hang.
		_ = enc.Encode(chatResponse{
			Model:      model,
			CreatedAt:  time.Now().UTC(),
			Message:    ollamaMessage{Role: "assistant", Content: "\n\n[claudama error: " + err.Error() + "]"},
			Done:       true,
			DoneReason: "error",
		})
		flusher.Flush()
		return
	}

	_ = enc.Encode(chatResponse{
		Model:           model,
		CreatedAt:       time.Now().UTC(),
		Message:         ollamaMessage{Role: "assistant", Content: ""},
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   time.Since(start).Nanoseconds(),
		PromptEvalCount: res.InputTokens,
		EvalCount:       res.OutputTokens,
	})
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
