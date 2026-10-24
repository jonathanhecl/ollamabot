package engine

import (
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
)

func toolNames(deps Deps) map[string]bool {
	registry := BuildRegistry(deps, "sess-1", nil)
	names := map[string]bool{}
	for _, def := range registry.Definitions() {
		names[def.Function.Name] = true
	}
	return names
}

func TestBuildRegistryExposesVoiceOnlyWithSpeechModel(t *testing.T) {
	base := config.Config{
		OllamaDefaultModel: "main-model",
		Workspace:          t.TempDir(),
		SessionsPath:       t.TempDir(),
	}

	if toolNames(Deps{ConfigMgr: config.NewManager(base)})["send_voice"] {
		t.Fatal("send_voice should be absent without a speech model")
	}

	withSpeech := base
	withSpeech.OllamaModelSpeech = "phonon:40m-es"
	if !toolNames(Deps{ConfigMgr: config.NewManager(withSpeech)})["send_voice"] {
		t.Fatal("send_voice should be exposed when a speech model is configured")
	}
}
