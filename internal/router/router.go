package router

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

const imageAnalysisPrompt = "Analyze this image in detail. Describe everything visible: objects, people, colors, text, spatial relationships, mood, style, and any other relevant information a language model would need to answer questions about it."

const audioAnalysisPrompt = "Analyze this audio. YOUR ABSOLUTE HIGHEST PRIORITY IS TO TRANSCRIBE ANY SPEECH VERBATIM. Start your response with the verbatim transcription of the speech. If the audio contains spoken words, transcribe them completely and accurately first. Do not summarize, paraphrase, or omit any spoken words. Only after the transcription, or if there is absolutely no speech, describe other sounds heard, speaker's tone, background noise, or other contextually relevant audio events."

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

// AnalyzeAudio sends a base64-encoded audio file to the audio model and returns
// a detailed textual description.
func (r *Router) AnalyzeAudio(ctx context.Context, base64data string, prompt string) (string, error) {
	model := r.audioModel()
	var effectivePrompt string
	if strings.TrimSpace(prompt) != "" {
		effectivePrompt = fmt.Sprintf("%s\n\nAdditional User Request/Context for this audio: \"%s\"", audioAnalysisPrompt, prompt)
	} else {
		effectivePrompt = audioAnalysisPrompt
	}
	log.Printf("[Router] AnalyzeAudio: model=%q, prompt_len=%d, data_len=%d", model, len(effectivePrompt), len(base64data))
	resp, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: effectivePrompt, Images: []string{base64data}},
		},
		Options: map[string]any{"temperature": 0, "num_ctx": 8000},
	})
	if err != nil {
		log.Printf("[Router] AnalyzeAudio FAILED: %v", err)
		return "", fmt.Errorf("audio analysis (%s): %w", model, err)
	}
	result := strings.TrimSpace(resp.Message.Content)
	log.Printf("[Router] AnalyzeAudio OK: result_len=%d", len(result))
	return result, nil
}

// NeedsMediaRouting reports whether there is a dedicated role model that
// differs from the main model for the given kind ("image" or "audio").
func (r *Router) NeedsMediaRouting(kind string) bool {
	switch kind {
	case "image":
		result := strings.TrimSpace(r.cfg.VisionModel) != "" && r.cfg.VisionModel != r.cfg.MainModel
		log.Printf("[Router] NeedsMediaRouting(%q): visionModel=%q, mainModel=%q → %v", kind, r.cfg.VisionModel, r.cfg.MainModel, result)
		return result
	case "audio":
		result := strings.TrimSpace(r.cfg.AudioModel) != "" && r.cfg.AudioModel != r.cfg.MainModel
		log.Printf("[Router] NeedsMediaRouting(%q): audioModel=%q, mainModel=%q → %v", kind, r.cfg.AudioModel, r.cfg.MainModel, result)
		return result
	}
	return false
}
