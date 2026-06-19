package config

import "testing"

func TestResolveModel(t *testing.T) {
	cfg := Config{
		OllamaDefaultModel:  "main-model",
		OllamaModelSubagent: "subagent-model",
		OllamaModelVision:   "vision-model",
		OllamaModelAudio:    "audio-model",
		OllamaModelEmbed:    "embed-model",
		OllamaModelLearning: "learning-model",
		OllamaModelImage:    "image-model",
	}

	tests := map[string]string{
		ModelRoleMain:       "main-model",
		ModelRoleSubagent:   "subagent-model",
		ModelRoleVision:     "vision-model",
		ModelRoleAudio:      "audio-model",
		ModelRoleEmbed:      "embed-model",
		ModelRoleEmbeddings: "embed-model",
		ModelRoleLearning:   "learning-model",
		ModelRoleImage:      "image-model",
	}
	for role, want := range tests {
		if got := ResolveModel(cfg, role); got != want {
			t.Fatalf("ResolveModel(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestResolveModelFallbacks(t *testing.T) {
	cfg := Config{OllamaDefaultModel: "main-model"}
	if got := ResolveModel(cfg, ModelRoleSubagent); got != "main-model" {
		t.Fatalf("subagent fallback = %q, want main-model", got)
	}
	if got := ResolveModel(cfg, ModelRoleLearning); got != "main-model" {
		t.Fatalf("learning fallback = %q, want main-model", got)
	}
	if got := ResolveModel(cfg, ModelRoleVision); got != "" {
		t.Fatalf("vision should not fall back implicitly, got %q", got)
	}
}
