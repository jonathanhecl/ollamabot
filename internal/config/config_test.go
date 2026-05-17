package config

import (
	"os"
	"reflect"
	"testing"
)

func TestLoadDefaultsAndEnvFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), ".env")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("OLLAMA_BASE_URL=http://localhost:11434/api/\nOLLAMA_PROBE_MODELS=qwen3:8b, nomic-embed-text:latest\nWEB_ADDR=\":9000\"\n")
	_ = file.Close()

	cfg, err := Load(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OllamaBaseURL != "http://localhost:11434" {
		t.Fatalf("base url = %q", cfg.OllamaBaseURL)
	}
	wantModels := []string{"qwen3:8b", "nomic-embed-text:latest"}
	if !reflect.DeepEqual(cfg.OllamaProbeModels, wantModels) {
		t.Fatalf("models = %#v", cfg.OllamaProbeModels)
	}
	if cfg.WebAddr != ":9000" {
		t.Fatalf("web addr = %q", cfg.WebAddr)
	}
}

func TestNormalizeBaseURLRejectsHostlessValue(t *testing.T) {
	if _, err := NormalizeBaseURL("localhost:11434"); err == nil {
		t.Fatal("expected error")
	}
}
