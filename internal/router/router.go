package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

const imageAnalysisPrompt = "Analyze this image in detail. Describe everything visible: objects, people, colors, text, spatial relationships, mood, style, and any other relevant information a language model would need to answer questions about it."

const audioAnalysisPrompt = "Analyze this audio in detail. Transcribe any speech verbatim. Describe all sounds heard, the speaker's tone and inferred emotional state, background sounds, and any notable events such as music, explosions, alarms, laughter, crying, or other sounds that may be contextually relevant."

// Config holds the role model assignments. Empty string means fall back to main.
type Config struct {
	MainModel   string
	VisionModel string
	AudioModel  string
}

// Router pre-processes media attachments using dedicated role models so the
// main model only receives text context.
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

// audioModel returns the effective model to use for audio analysis.
func (r *Router) audioModel() string {
	if strings.TrimSpace(r.cfg.AudioModel) != "" {
		return r.cfg.AudioModel
	}
	return r.cfg.MainModel
}

// AnalyzeImage sends a base64-encoded image to the vision model and returns a
// detailed textual description.
func (r *Router) AnalyzeImage(ctx context.Context, base64data string) (string, error) {
	model := r.visionModel()
	resp, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: imageAnalysisPrompt, Images: []string{base64data}},
		},
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return "", fmt.Errorf("image analysis (%s): %w", model, err)
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

// AnalyzeAudio sends a base64-encoded audio file to the audio model and returns
// a detailed textual description.
func (r *Router) AnalyzeAudio(ctx context.Context, base64data string) (string, error) {
	model := r.audioModel()
	resp, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: audioAnalysisPrompt, Images: []string{base64data}},
		},
		Options: map[string]any{"temperature": 0, "num_ctx": 8000},
	})
	if err != nil {
		return "", fmt.Errorf("audio analysis (%s): %w", model, err)
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

// NeedsMediaRouting reports whether there is a dedicated role model that
// differs from the main model for the given kind ("image" or "audio").
func (r *Router) NeedsMediaRouting(kind string) bool {
	switch kind {
	case "image":
		return strings.TrimSpace(r.cfg.VisionModel) != "" && r.cfg.VisionModel != r.cfg.MainModel
	case "audio":
		return strings.TrimSpace(r.cfg.AudioModel) != "" && r.cfg.AudioModel != r.cfg.MainModel
	}
	return false
}
