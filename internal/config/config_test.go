package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func init() {
	checkOllamaModelsHook = func(string) ([]string, error) {
		return []string{"gemma4:e4b"}, nil
	}
}

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

func TestNormalizeBaseURLForgivesHostlessValue(t *testing.T) {
	got, err := NormalizeBaseURL("localhost:11434")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "http://localhost:11434" {
		t.Fatalf("expected http://localhost:11434, got %q", got)
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

func TestLoadStartupNotification(t *testing.T) {
	tests := []struct {
		name  string
		lines string
		want  bool
	}{
		{name: "true", lines: "TELEGRAM_BOT_TOKEN=x\nTELEGRAM_STARTUP_NOTIFICATION=true\n", want: true},
		{name: "false", lines: "TELEGRAM_BOT_TOKEN=x\nTELEGRAM_STARTUP_NOTIFICATION=false\n", want: false},
		{name: "absent defaults false", lines: "TELEGRAM_BOT_TOKEN=x\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir() + "/.env"
			if err := os.WriteFile(path, []byte(tt.lines), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TelegramStartupNotification != tt.want {
				t.Fatalf("StartupNotification = %v, want %v", cfg.TelegramStartupNotification, tt.want)
			}
		})
	}
}

func TestSaveBasicLoadRoundTrip(t *testing.T) {
	// Env vars take precedence over the file in Load, so clear them to
	// prove the file itself round-trips every persisted setting.
	keys := []string{
		"OLLAMA_BASE_URL", "OLLAMA_PROBE_MODELS", "OLLAMA_DEFAULT_MODEL",
		"OLLAMA_MODEL_VISION", "OLLAMA_MODEL_AUDIO", "OLLAMA_MODEL_EMBED",
		"OLLAMA_MODEL_IMAGE", "OLLAMA_MODEL_SPEECH", "OLLAMA_IMAGE_STEPS",
		"TELEGRAM_BOT_TOKEN", "TELEGRAM_AUTHORIZED_IDS", "SESSION_EXPIRY_MIN",
		"TELEGRAM_STARTUP_NOTIFICATION", "SERVER_ENABLED", "WEB_ENABLED",
		"SERVER_PORT", "WEB_PORT", "WEB_ADDR", "WEB_SEARCH_ENABLED",
		"SERVER_EXPOSE_NETWORK", "WEB_EXPOSE_NETWORK", "SESSION_AUTO_NAME",
		"WEB_AUTO_NAME", "WORKSPACE_PATH", "SESSIONS_PATH", "MEMORY_PATH",
		"SKILLS_PATH", "SLEEP_MODE_ENABLED", "SLEEP_MODE_INACTIVITY_THRESHOLD",
		"SLEEP_MODE_RESUME_DELAY", "OLLAMA_MODEL_LEARNING",
		"SLEEP_MODE_SUBAGENTS_ENABLED", "OLLAMA_MODEL_SUBAGENT",
		"OLLAMA_THINK_ENABLED", "OLLAMA_MAX_TOKENS", "OLLAMA_MAX_CONTEXT",
		"SERVER_PASSWORD", "SESSION_PLAN_CONFIRMATION", "PLAN_CONFIRMATION",
		"SUBAGENT_TIMEOUT_MINUTES", "AUTONOMOUS_STALE_TASK_MINUTES",
		"AUTONOMOUS_VERIFICATION_ENABLED", "WEB_SEARCH_PRIORITY",
		"SEARCH_PROVIDERS", "WEB_SEARCH_BRAVE_API_KEY", "WEB_SEARCH_TAVILY_API_KEY",
	}
	saved := map[string]string{}
	had := map[string]bool{}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			saved[k], had[k] = v, true
			t.Cleanup(func() {})
		}
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, k := range keys {
			if had[k] {
				_ = os.Setenv(k, saved[k])
			}
		}
	})

	want := Config{
		OllamaBaseURL:                 "http://192.168.1.10:11434",
		OllamaProbeModels:             []string{"probe-a", "probe-b"},
		OllamaDefaultModel:            "roundtrip-main",
		OllamaModelVision:             "roundtrip-vision",
		OllamaModelAudio:              "roundtrip-audio",
		OllamaModelEmbed:              "roundtrip-embed",
		OllamaModelImage:              "roundtrip-image",
		OllamaModelSpeech:             "roundtrip-speech",
		OllamaSpeechVoice:             "roundtrip-voice",
		OllamaModelLearning:           "roundtrip-learning",
		OllamaModelSubagent:           "roundtrip-subagent",
		OllamaImageSteps:              7,
		OllamaThinkEnabled:            false,
		OllamaMaxTokens:               4096,
		OllamaMaxContext:              8192,
		ServerEnabled:                 false,
		ServerPort:                    "9091",
		ServerPassword:                "s3cret",
		ServerExposeNetwork:           false,
		SessionAutoName:               false,
		TelegramBotToken:              "roundtrip-token",
		TelegramAuthorizedIDs:         []string{"111", "222"},
		TelegramSessionExpiryMin:      60,
		TelegramStartupNotification:   true,
		Workspace:                     "ws-rt",
		WorkspaceRaw:                  "ws-rt",
		SessionsPath:                  "sess-rt",
		SessionsPathRaw:               "sess-rt",
		MemoryPath:                    "mem-rt",
		MemoryPathRaw:                 "mem-rt",
		SkillsPath:                    "sk-rt",
		SkillsPathRaw:                 "sk-rt",
		SleepModeEnabled:              true,
		SleepModeInactivityThreshold:  "45m",
		SleepModeResumeDelay:          "5m",
		SleepModeSubagentsEnabled:     true,
		SearchProviders:               []string{"brave", "tavily"},
		BraveSearchAPIKey:             "brave-rt",
		TavilyAPIKey:                  "tavily-rt",
		WebSearchEnabled:              true,
		PlanConfirmation:              "always",
		SubagentTimeoutMinutes:        15,
		AutonomousStaleTaskMinutes:    45,
		AutonomousVerificationEnabled: false,
	}

	path := t.TempDir() + "/.env"
	if err := SaveBasic(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestCreateInteractiveWebOnly(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("y\n9090\nn\nn\nhttp://localhost:11434\n\n\n")
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
	input := strings.NewReader("y\n8080\nn\nn\nhttp://192.168.1.10:11434\n\n\n")
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
	input := strings.NewReader("y\n8080\ny\ny\nsecret\nhttp://localhost:11434\n\nbot-token\n\n")
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
	input := strings.NewReader("n\nhttp://192.168.0.50:11434\n\nbot-token\n\n")
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

func TestCreateInteractiveTelegramWithAuthorizedIDs(t *testing.T) {
	path := t.TempDir() + "/.env"
	input := strings.NewReader("n\nhttp://localhost:11434\n\nbot-token\n123456789, 987654321\n")
	var output strings.Builder
	if err := CreateInteractive(path, input, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"123456789", "987654321"}
	if !reflect.DeepEqual(cfg.TelegramAuthorizedIDs, want) {
		t.Fatalf("telegram authorized ids = %#v", cfg.TelegramAuthorizedIDs)
	}
}

func TestCreateInteractiveAllDefaultsExplicitNoWebNoTelegram(t *testing.T) {
	oldHook := checkOllamaModelsHook
	checkOllamaModelsHook = func(string) ([]string, error) {
		return nil, nil
	}
	defer func() { checkOllamaModelsHook = oldHook }()

	path := t.TempDir() + "/.env"
	input := strings.NewReader("n\n\n\n\n")
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
