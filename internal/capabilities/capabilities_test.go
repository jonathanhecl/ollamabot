package capabilities

import (
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestFromOllamaMapsReportedAndInferredCapabilities(t *testing.T) {
	report := FromOllama(ollama.ModelTag{
		Name: "model",
		Details: ollama.ModelDetails{
			Families:          []string{"clip", "gemma4"},
			ParameterSize:     "4B",
			QuantizationLevel: "Q4",
		},
	}, ollama.ShowResponse{
		Capabilities: []string{"completion", "vision"},
		ProjectorInfo: map[string]any{
			"clip.has_audio_encoder":  true,
			"clip.has_vision_encoder": true,
		},
		ModelInfo: map[string]any{"gemma4.context_length": float64(131072)},
	})

	if report.Capabilities["completion"] != Confirmed {
		t.Fatalf("completion = %s", report.Capabilities["completion"])
	}
	if report.Capabilities["audio"] != Inferred {
		t.Fatalf("audio = %s", report.Capabilities["audio"])
	}
	if report.ContextLength != 131072 {
		t.Fatalf("context = %d", report.ContextLength)
	}
}

func TestFromOllamaExtractsVoices(t *testing.T) {
	report := FromOllama(ollama.ModelTag{Name: "phonon:40m-es"}, ollama.ShowResponse{
		Capabilities: []string{"speech"},
		Voices:       []string{"Zoey", "Easton", "Freya"},
	})
	if len(report.Voices) != 3 || report.Voices[0] != "Zoey" {
		t.Fatalf("voices = %#v, want [Zoey Easton Freya]", report.Voices)
	}

	reportFallback := FromOllama(ollama.ModelTag{Name: "phonon:custom"}, ollama.ShowResponse{
		Capabilities: []string{"speech"},
		ModelInfo: map[string]any{
			"voices": []any{"Alice", "Bob"},
		},
	})
	if len(reportFallback.Voices) != 2 || reportFallback.Voices[0] != "Alice" {
		t.Fatalf("voices from model_info = %#v, want [Alice Bob]", reportFallback.Voices)
	}
}
