package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadDefaultsAndEnvFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), ".env")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("OLLAMA_BASE_URL=http://localhost:11434/api/\nOLLAMA_PROBE_MODELS=qwen3:8b, nomic-embed-text:latest\nWEB_PORT=\"9000\"\n")
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
	if cfg.ServerPort != "9000" {
		t.Fatalf("server port = %q", cfg.ServerPort)
	}
	if !cfg.ServerEnabled {
		t.Fatal("web should default to enabled")
	}
}

func TestNormalizeBaseURLRejectsHostlessValue(t *testing.T) {
	if _, err := NormalizeBaseURL("localhost:11434"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateInteractive(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("http://localhost:11434\nn\n9090\n")
	var output strings.Builder
	if err := CreateInteractive(path, input, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerEnabled {
		t.Fatal("web should be disabled")
	}
	if cfg.ServerPort != "9090" {
		t.Fatalf("server port = %q", cfg.ServerPort)
	}
}
