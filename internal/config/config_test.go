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

func TestDefaultEnvPath(t *testing.T) {
	path := DefaultEnvPath()
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if !strings.HasSuffix(path, ".env") {
		t.Fatalf("expected .env suffix, got %q", path)
	}
}

func TestCreateInteractiveWebOnly(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("y\nhttp://localhost:11434\n9090\nn\nn\n\n")
	var output strings.Builder
	if err := CreateInteractive(path, input, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ServerEnabled {
		t.Fatal("web should be enabled")
	}
	if cfg.ServerPort != "9090" {
		t.Fatalf("server port = %q", cfg.ServerPort)
	}
	if cfg.ServerExposeNetwork {
		t.Fatal("network exposure should be disabled")
	}
	if cfg.ServerPassword != "" {
		t.Fatal("password should be empty")
	}
	if cfg.TelegramBotToken != "" {
		t.Fatal("telegram token should be empty")
	}
	if cfg.OllamaBaseURL != "http://localhost:11434" {
		t.Fatalf("ollama base url = %q", cfg.OllamaBaseURL)
	}
}

func TestCreateInteractiveWebCustomOllamaURL(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("y\nhttp://192.168.1.10:11434\n8080\nn\nn\n\n")
	var output strings.Builder
	if err := CreateInteractive(path, input, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OllamaBaseURL != "http://192.168.1.10:11434" {
		t.Fatalf("ollama base url = %q", cfg.OllamaBaseURL)
	}
}

func TestCreateInteractiveWebWithPasswordAndTelegram(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("y\nhttp://localhost:11434\n8080\ny\ny\nsecret\nbot-token\n")
	var output strings.Builder
	if err := CreateInteractive(path, input, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ServerEnabled {
		t.Fatal("web should be enabled")
	}
	if !cfg.ServerExposeNetwork {
		t.Fatal("network exposure should be enabled")
	}
	if cfg.ServerPassword != "secret" {
		t.Fatalf("password = %q", cfg.ServerPassword)
	}
	if cfg.TelegramBotToken != "bot-token" {
		t.Fatalf("telegram token = %q", cfg.TelegramBotToken)
	}
}

func TestCreateInteractiveTelegramOnly(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("n\nhttp://192.168.0.50:11434\nbot-token\n")
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
	if cfg.OllamaBaseURL != "http://192.168.0.50:11434" {
		t.Fatalf("ollama base url = %q", cfg.OllamaBaseURL)
	}
	if cfg.TelegramBotToken != "bot-token" {
		t.Fatalf("telegram token = %q", cfg.TelegramBotToken)
	}
}

func TestCreateInteractiveAllDefaultsExplicitNoWebNoTelegram(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("n\n\n\n")
	var output strings.Builder
	if err := CreateInteractive(path, input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No channels configured") {
		t.Fatal("expected no-channels notice")
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	assertDefaultInteractiveConfig(t, cfg, "http://localhost:11434", false, "")
}

func assertDefaultInteractiveConfig(t *testing.T, cfg Config, baseURL string, serverEnabled bool, telegramToken string) {
	t.Helper()

	if cfg.OllamaBaseURL != baseURL {
		t.Fatalf("ollama base url = %q", cfg.OllamaBaseURL)
	}
	if cfg.OllamaDefaultModel != "" {
		t.Fatalf("default model = %q", cfg.OllamaDefaultModel)
	}
	if len(cfg.OllamaProbeModels) != 0 {
		t.Fatalf("probe models = %#v", cfg.OllamaProbeModels)
	}
	if cfg.OllamaModelVision != "" || cfg.OllamaModelAudio != "" || cfg.OllamaModelEmbed != "" ||
		cfg.OllamaModelImage != "" || cfg.OllamaModelLearning != "" || cfg.OllamaModelSubagent != "" {
		t.Fatal("role models should be empty")
	}
	if cfg.OllamaImageSteps != 4 {
		t.Fatalf("image steps = %d", cfg.OllamaImageSteps)
	}
	if !cfg.OllamaThinkEnabled {
		t.Fatal("think should be enabled by default")
	}
	if cfg.ServerEnabled != serverEnabled {
		t.Fatalf("server enabled = %v", cfg.ServerEnabled)
	}
	if cfg.ServerPort != "8080" {
		t.Fatalf("server port = %q", cfg.ServerPort)
	}
	if cfg.ServerPassword != "" {
		t.Fatalf("server password = %q", cfg.ServerPassword)
	}
	if cfg.ServerExposeNetwork {
		t.Fatal("network exposure should be disabled by default")
	}
	if !cfg.SessionAutoName {
		t.Fatal("session auto name should be enabled")
	}
	if cfg.TelegramSessionExpiryMin != 30 {
		t.Fatalf("session expiry = %d", cfg.TelegramSessionExpiryMin)
	}
	if cfg.PlanConfirmation != "smart" {
		t.Fatalf("plan confirmation = %q", cfg.PlanConfirmation)
	}
	if cfg.TelegramBotToken != telegramToken {
		t.Fatalf("telegram token = %q", cfg.TelegramBotToken)
	}
	if len(cfg.TelegramAuthorizedIDs) != 0 {
		t.Fatalf("authorized ids = %#v", cfg.TelegramAuthorizedIDs)
	}
	if cfg.TelegramStartupNotification {
		t.Fatal("startup notification should be disabled")
	}
	if cfg.WorkspaceRaw != "workspace" || cfg.SessionsPathRaw != "sessions" ||
		cfg.MemoryPathRaw != "memory" || cfg.SkillsPathRaw != "skills" {
		t.Fatalf("paths = %#v", cfg)
	}
	if cfg.SleepModeEnabled || cfg.SleepModeSubagentsEnabled {
		t.Fatal("sleep mode should be disabled")
	}
	if cfg.SleepModeInactivityThreshold != "30m" || cfg.SleepModeResumeDelay != "10m" {
		t.Fatalf("sleep timings = %q / %q", cfg.SleepModeInactivityThreshold, cfg.SleepModeResumeDelay)
	}
	if cfg.WebSearchEnabled {
		t.Fatal("web search should be disabled")
	}
	if len(cfg.SearchProviders) != 1 || cfg.SearchProviders[0] != "ddg" {
		t.Fatalf("search providers = %#v", cfg.SearchProviders)
	}
	if cfg.BraveSearchAPIKey != "" || cfg.TavilyAPIKey != "" {
		t.Fatal("search api keys should be empty")
	}
}
