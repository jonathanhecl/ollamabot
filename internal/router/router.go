package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

const imageAnalysisPrompt = "Analyze this image in detail. Describe everything visible: objects, people, colors, text, spatial relationships, mood, style, and any other relevant information a language model would need to answer questions about it."

// Keep this prompt SHORT: long, verbose prompts make multimodal models (e.g.
// gemma4) return empty transcriptions when combined with structured output.
// Field semantics are carried by the JSON schema descriptions instead.
const audioTranscriptionPrompt = "Listen to the attached audio. Return JSON with the verbatim transcription of the speech, the language code, and any relevant non-speech sounds."

// Config holds the role model assignments. Empty string means fall back to main.
type Config struct {
	MainModel   string
	VisionModel string
	AudioModel  string
	ImageModel  string
	ImageSteps  int
	// HasCapability reports whether a model supports a capability ("audio" or
	// "vision"), based on probed/confirmed data. Nil means capabilities are
	// unknown and routing falls back to config-equality heuristics.
	HasCapability func(model, capability string) bool
}

// Decision describes how a media attachment of a given kind must be handled.
type Decision string

const (
	// DecisionPassthrough sends the raw attachment directly to the main model.
	DecisionPassthrough Decision = "passthrough"
	// DecisionRoute pre-processes the attachment with a dedicated role model.
	DecisionRoute Decision = "route"
	// DecisionUnsupported drops the attachment: no configured model supports it.
	DecisionUnsupported Decision = "unsupported"
)

// AudioTranscription is the structured output produced by the audio role model.
type AudioTranscription struct {
	Transcription string `json:"transcription"`
	Language      string `json:"language,omitempty"`
	Sounds        string `json:"sounds,omitempty"`
	Unreadable    bool   `json:"unreadable"`
}

var audioTranscriptionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"transcription": map[string]any{
			"type":        "string",
			"description": "Verbatim transcription of all speech in the audio, in the original language. Do not summarize or translate. Empty string if there is no speech.",
		},
		"language": map[string]any{
			"type":        "string",
			"description": "ISO 639-1 code of the spoken language. Empty string if no speech.",
		},
		"sounds": map[string]any{
			"type":        "string",
			"description": "Brief description of relevant non-speech sounds. Empty string if none.",
		},
		"unreadable": map[string]any{
			"type":        "boolean",
			"description": "True only if the audio could not be processed or understood at all.",
		},
	},
	"required": []string{"transcription", "language", "sounds", "unreadable"},
}

// Router pre-processes media attachments using role models so the main model
// receives text context (or raw media when it supports it natively).
type Router struct {
	client *ollama.Client
	cfg    Config
}

func New(client *ollama.Client, cfg Config) *Router {
	return &Router{client: client, cfg: cfg}
}

// visionModel returns the effective model to use for image analysis.
func (r *Router) visionModel() string {
	if strings.TrimSpace(r.cfg.VisionModel) != "" {
		return r.cfg.VisionModel
	}
	return r.cfg.MainModel
}

// audioModel returns the effective model to use for audio transcription.
func (r *Router) audioModel() string {
	if strings.TrimSpace(r.cfg.AudioModel) != "" {
		return r.cfg.AudioModel
	}
	return r.cfg.MainModel
}

// imageModel returns the effective model to use for image generation.
func (r *Router) imageModel() string {
	if strings.TrimSpace(r.cfg.ImageModel) != "" {
		return r.cfg.ImageModel
	}
	return r.cfg.MainModel
}

// imageSteps returns the number of steps for image generation.
func (r *Router) imageSteps() int {
	if r.cfg.ImageSteps > 0 {
		return r.cfg.ImageSteps
	}
	return 4
}

// ImageProgressCallback is called during image generation with progress updates
type ImageProgressCallback func(completed, total int, status string)

// GenerateImage generates an image using the configured image model with streaming progress.
// Returns the base64-encoded image data.
func (r *Router) GenerateImage(ctx context.Context, prompt string, width, height, seed int, onProgress ImageProgressCallback) (string, error) {
	model := r.imageModel()
	steps := r.imageSteps()

	if strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("no image model configured")
	}

	log.Printf("[Router] GenerateImage: model=%q, prompt_len=%d, size=%dx%d, steps=%d", model, len(prompt), width, height, steps)

	req := ollama.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		Options: map[string]any{
			"width":  width,
			"height": height,
			"steps":  steps,
			"seed":   seed,
		},
	}

	var finalImage string
	err := r.client.GenerateStream(ctx, req, func(chunk ollama.GenerateResponse) error {
		// Progress update
		if chunk.Total > 0 && onProgress != nil {
			onProgress(chunk.Completed, chunk.Total, "generating")
		}
		// Final response contains the image
		if chunk.Done && chunk.Response != "" {
			finalImage = chunk.Response
		}
		return nil
	})

	if err != nil {
		log.Printf("[Router] GenerateImage FAILED: %v", err)
		return "", fmt.Errorf("image generation (%s): %w", model, err)
	}

	log.Printf("[Router] GenerateImage OK: result_len=%d", len(finalImage))
	return finalImage, nil
}

// Decide reports how attachments of the given kind ("image" or "audio") must
// be handled for the current model configuration:
//   - a dedicated role model different from main → route (pre-process)
//   - main supports the capability natively → passthrough
//   - nobody supports it → unsupported (graceful drop)
func (r *Router) Decide(kind string) Decision {
	var dedicated, capability string
	switch kind {
	case "image":
		dedicated, capability = strings.TrimSpace(r.cfg.VisionModel), "vision"
	case "audio":
		dedicated, capability = strings.TrimSpace(r.cfg.AudioModel), "audio"
	default:
		return DecisionUnsupported
	}

	main := strings.TrimSpace(r.cfg.MainModel)
	decision := DecisionPassthrough
	switch {
	case dedicated != "" && dedicated != main:
		decision = DecisionRoute
	case r.cfg.HasCapability == nil:
		// Capabilities unknown: legacy behavior, trust the main model.
		decision = DecisionPassthrough
	case r.cfg.HasCapability(main, capability):
		decision = DecisionPassthrough
	case dedicated != "" && dedicated == main:
		// User explicitly forced main as the role model: trust it.
		decision = DecisionPassthrough
	default:
		decision = DecisionUnsupported
	}
	log.Printf("[Router] Decide(%q): dedicated=%q, main=%q → %s", kind, dedicated, main, decision)
	return decision
}

// AnalyzeImage sends a base64-encoded image to the vision model and returns a
// detailed textual description.
func (r *Router) AnalyzeImage(ctx context.Context, base64data string, prompt string) (string, error) {
	model := r.visionModel()
	effectivePrompt := imageAnalysisPrompt
	if strings.TrimSpace(prompt) != "" {
		effectivePrompt = prompt
	}
	log.Printf("[Router] AnalyzeImage: model=%q, prompt_len=%d, data_len=%d", model, len(effectivePrompt), len(base64data))
	resp, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: effectivePrompt, Images: []string{base64data}},
		},
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		log.Printf("[Router] AnalyzeImage FAILED: %v", err)
		return "", fmt.Errorf("image analysis (%s): %w", model, err)
	}
	result := strings.TrimSpace(resp.Message.Content)
	log.Printf("[Router] AnalyzeImage OK: result_len=%d", len(result))
	return result, nil
}

// TranscribeAudio sends a base64-encoded audio file to the effective audio
// model and returns a structured transcription. The request uses a JSON schema
// (structured output) with temperature 0 so the result is replicable. The user
// prompt is intentionally NOT mixed in: the transcription must depend only on
// the audio; interpretation is the main model's job.
func (r *Router) TranscribeAudio(ctx context.Context, base64data string) (AudioTranscription, error) {
	if len(strings.TrimSpace(base64data)) == 0 {
		return AudioTranscription{}, fmt.Errorf("audio transcription: empty base64 data provided")
	}
	model := r.audioModel()
	log.Printf("[Router] TranscribeAudio: model=%q, data_len=%d", model, len(base64data))

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		resp, err := r.client.Chat(ctx, ollama.ChatRequest{
			Model: model,
			Messages: []ollama.Message{
				{Role: "user", Content: audioTranscriptionPrompt, Images: []string{base64data}},
			},
			Format:  audioTranscriptionSchema,
			Options: map[string]any{"temperature": 0, "num_ctx": 8000},
		})
		if err != nil {
			log.Printf("[Router] TranscribeAudio FAILED (attempt %d): %v", attempt, err)
			lastErr = fmt.Errorf("audio transcription (%s): %w", model, err)
			continue
		}
		raw := strings.TrimSpace(resp.Message.Content)
		var out AudioTranscription
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			log.Printf("[Router] TranscribeAudio: invalid JSON (attempt %d, len=%d): %v", attempt, len(raw), err)
			lastErr = fmt.Errorf("audio transcription (%s): invalid JSON output: %w", model, err)
			// Last resort on final attempt: treat the raw text as transcription.
			if attempt == 2 && raw != "" {
				log.Printf("[Router] TranscribeAudio: falling back to raw text as transcription")
				return AudioTranscription{Transcription: raw}, nil
			}
			continue
		}
		out.Transcription = strings.TrimSpace(out.Transcription)
		out.Sounds = normalizeNone(out.Sounds)
		out.Language = normalizeNone(out.Language)
		if out.Transcription == "" && out.Sounds == "" {
			out.Unreadable = true
		}
		log.Printf("[Router] TranscribeAudio OK: transcription_len=%d, language=%q, unreadable=%v", len(out.Transcription), out.Language, out.Unreadable)
		return out, nil
	}
	return AudioTranscription{}, lastErr
}

// normalizeNone clears placeholder values some models emit for empty fields.
func normalizeNone(s string) string {
	trimmed := strings.TrimSpace(s)
	switch strings.ToLower(strings.TrimRight(trimmed, ".")) {
	case "", "none", "n/a", "no", "null", "nothing":
		return ""
	}
	return trimmed
}
