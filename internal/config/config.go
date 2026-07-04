package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	OllamaBaseURL                string
	OllamaProbeModels            []string
	OllamaDefaultModel           string
	OllamaModelVision            string
	OllamaModelAudio             string
	OllamaModelEmbed             string
	OllamaModelImage             string
	OllamaImageSteps             int
	TelegramBotToken             string
	TelegramAuthorizedIDs        []string
	TelegramSessionExpiryMin     int
	TelegramStartupNotification  bool
	ServerPort                   string
	ServerEnabled                bool
	WebSearchEnabled             bool
	ServerExposeNetwork          bool
	SessionAutoName              bool
	Workspace                    string
	WorkspaceRaw                 string
	SessionsPath                 string
	SessionsPathRaw              string
	MemoryPath                   string
	MemoryPathRaw                string
	SkillsPath                   string
	SkillsPathRaw                string
	SleepModeEnabled             bool
	SleepModeInactivityThreshold string
	SleepModeResumeDelay         string
	OllamaModelLearning          string
	SearchProviders              []string // ordered list: "brave", "tavily", "ddg"
	BraveSearchAPIKey            string
	TavilyAPIKey                 string
	SleepModeSubagentsEnabled    bool
	OllamaModelSubagent          string
	OllamaThinkEnabled           bool
	OllamaMaxTokens              int
	OllamaMaxContext             int
	ServerPassword               string
	PlanConfirmation             string
	SubagentTimeoutMinutes       int
}

func Load(path string) (Config, error) {
	values := map[string]string{}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return Config{}, err
			}
		} else {
			defer file.Close()
			parsed, err := Parse(file)
			if err != nil {
				return Config{}, err
			}
			values = parsed
		}
	}

	cfg := Config{
		OllamaBaseURL:                "http://localhost:11434",
		ServerPort:                   "8080",
		ServerEnabled:                true,
		WebSearchEnabled:             false,
		ServerExposeNetwork:          true,
		SessionAutoName:              true,
		Workspace:                    "workspace",
		WorkspaceRaw:                 "workspace",
		SessionsPath:                 "sessions",
		SessionsPathRaw:              "sessions",
		MemoryPath:                   "memory",
		MemoryPathRaw:                "memory",
		SkillsPath:                   "skills",
		SkillsPathRaw:                "skills",
		SleepModeEnabled:             false,
		SleepModeInactivityThreshold: "30m",
		SleepModeResumeDelay:         "10m",
		OllamaModelLearning:          "",
		SearchProviders:              []string{"ddg"},
		BraveSearchAPIKey:            "",
		TavilyAPIKey:                 "",
		SleepModeSubagentsEnabled:    false,
		OllamaModelSubagent:          "",
		OllamaThinkEnabled:           true,
		OllamaMaxTokens:              16384,
		OllamaMaxContext:             0,
		ServerPassword:               "",
		TelegramSessionExpiryMin:     30,
		PlanConfirmation:             "smart",
		SubagentTimeoutMinutes:       10,
	}
	apply := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return values[key]
	}
	if value := apply("OLLAMA_BASE_URL"); value != "" {
		cfg.OllamaBaseURL = value
	}
	cfg.OllamaProbeModels = splitCSV(apply("OLLAMA_PROBE_MODELS"))
	cfg.OllamaDefaultModel = apply("OLLAMA_DEFAULT_MODEL")
	cfg.OllamaModelVision = apply("OLLAMA_MODEL_VISION")
	cfg.OllamaModelAudio = apply("OLLAMA_MODEL_AUDIO")
	cfg.OllamaModelEmbed = apply("OLLAMA_MODEL_EMBED")
	cfg.OllamaModelImage = apply("OLLAMA_MODEL_IMAGE")
	if value := apply("OLLAMA_IMAGE_STEPS"); value != "" {
		if val, err := strconv.Atoi(value); err == nil {
			cfg.OllamaImageSteps = val
		}
	}
	if cfg.OllamaImageSteps <= 0 {
		cfg.OllamaImageSteps = 4
	}
	cfg.TelegramBotToken = apply("TELEGRAM_BOT_TOKEN")
	cfg.TelegramAuthorizedIDs = splitCSV(apply("TELEGRAM_AUTHORIZED_IDS"))
	if value := apply("SESSION_EXPIRY_MIN"); value != "" {
		if val, err := strconv.Atoi(value); err == nil {
			cfg.TelegramSessionExpiryMin = val
		}
	}
	if value := apply("SERVER_ENABLED"); value != "" {
		cfg.ServerEnabled = parseBool(value)
	} else if value := apply("WEB_ENABLED"); value != "" {
		cfg.ServerEnabled = parseBool(value)
	}
	if value := apply("SERVER_PORT"); value != "" {
		cfg.ServerPort = value
	} else if value := apply("WEB_PORT"); value != "" {
		cfg.ServerPort = value
	} else if value := apply("WEB_ADDR"); value != "" {
		cfg.ServerPort = strings.TrimPrefix(strings.TrimSpace(value), ":")
	}
	if value := apply("WEB_SEARCH_ENABLED"); value != "" {
		cfg.WebSearchEnabled = parseBool(value)
	}
	if value := apply("SERVER_EXPOSE_NETWORK"); value != "" {
		cfg.ServerExposeNetwork = parseBool(value)
	} else if value := apply("WEB_EXPOSE_NETWORK"); value != "" {
		cfg.ServerExposeNetwork = parseBool(value)
	}
	if value := apply("SESSION_AUTO_NAME"); value != "" {
		cfg.SessionAutoName = parseBool(value)
	} else if value := apply("WEB_AUTO_NAME"); value != "" {
		cfg.SessionAutoName = parseBool(value)
	}
	if value := apply("WORKSPACE_PATH"); value != "" {
		cfg.Workspace = value
		cfg.WorkspaceRaw = value
	}
	if value := apply("SESSIONS_PATH"); value != "" {
		cfg.SessionsPath = value
		cfg.SessionsPathRaw = value
	}
	if value := apply("MEMORY_PATH"); value != "" {
		cfg.MemoryPath = value
		cfg.MemoryPathRaw = value
	}
	if value := apply("SKILLS_PATH"); value != "" {
		cfg.SkillsPath = value
		cfg.SkillsPathRaw = value
	}
	if value := apply("SLEEP_MODE_ENABLED"); value != "" {
		cfg.SleepModeEnabled = parseBool(value)
	}
	if value := apply("SLEEP_MODE_INACTIVITY_THRESHOLD"); value != "" {
		cfg.SleepModeInactivityThreshold = value
	}
	if value := apply("SLEEP_MODE_RESUME_DELAY"); value != "" {
		cfg.SleepModeResumeDelay = value
	}
	if value := apply("OLLAMA_MODEL_LEARNING"); value != "" {
		cfg.OllamaModelLearning = value
	}
	if value := apply("SLEEP_MODE_SUBAGENTS_ENABLED"); value != "" {
		cfg.SleepModeSubagentsEnabled = parseBool(value)
	}
	if value := apply("OLLAMA_MODEL_SUBAGENT"); value != "" {
		cfg.OllamaModelSubagent = value
	}
	if value := apply("OLLAMA_THINK_ENABLED"); value != "" {
		cfg.OllamaThinkEnabled = parseBool(value)
	}
	if value := apply("OLLAMA_MAX_TOKENS"); value != "" {
		if val, err := strconv.Atoi(value); err == nil && val > 0 {
			cfg.OllamaMaxTokens = val
		}
	}
	if value := apply("OLLAMA_MAX_CONTEXT"); value != "" {
		if val, err := strconv.Atoi(value); err == nil && val > 0 {
			cfg.OllamaMaxContext = val
		}
	}
	if value := apply("SERVER_PASSWORD"); value != "" {
		cfg.ServerPassword = value
	}
	if value := apply("SESSION_PLAN_CONFIRMATION"); value != "" {
		cfg.PlanConfirmation = value
	} else if value := apply("PLAN_CONFIRMATION"); value != "" {
		cfg.PlanConfirmation = value
	}
	if value := apply("SUBAGENT_TIMEOUT_MINUTES"); value != "" {
		if val, err := strconv.Atoi(value); err == nil && val > 0 {
			cfg.SubagentTimeoutMinutes = val
		}
	}
	if value := apply("WEB_SEARCH_PRIORITY"); value != "" {
		cfg.SearchProviders = splitCSV(value)
	} else if value := apply("SEARCH_PROVIDERS"); value != "" {
		cfg.SearchProviders = splitCSV(value)
	}
	if value := apply("WEB_SEARCH_BRAVE_API_KEY"); value != "" {
		cfg.BraveSearchAPIKey = value
	}
	if value := apply("WEB_SEARCH_TAVILY_API_KEY"); value != "" {
		cfg.TavilyAPIKey = value
	}
	// Infer WebSearchEnabled from providers list
	if len(cfg.SearchProviders) > 0 && !(len(cfg.SearchProviders) == 1 && cfg.SearchProviders[0] == "none") {
		// keep WebSearchEnabled as-is; it is used as the main gate
	}

	normalized, err := NormalizeBaseURL(cfg.OllamaBaseURL)
	if err != nil {
		return Config{}, err
	}
	cfg.OllamaBaseURL = normalized
	return cfg, nil
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DefaultEnvPath returns the .env path next to the running executable.
func DefaultEnvPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ".env"
	}
	return filepath.Join(filepath.Dir(exe), ".env")
}

func defaultInteractiveConfig(baseURL string) Config {
	return Config{
		OllamaBaseURL:                baseURL,
		OllamaDefaultModel:           "",
		OllamaProbeModels:            nil,
		OllamaModelVision:            "",
		OllamaModelAudio:             "",
		OllamaModelEmbed:             "",
		OllamaModelImage:             "",
		OllamaModelLearning:          "",
		OllamaModelSubagent:          "",
		OllamaImageSteps:             4,
		OllamaThinkEnabled:           true,
		ServerEnabled:                false,
		ServerPort:                   "8080",
		ServerPassword:               "",
		ServerExposeNetwork:          false,
		SessionAutoName:              true,
		TelegramSessionExpiryMin:     30,
		TelegramBotToken:             "",
		TelegramAuthorizedIDs:        nil,
		TelegramStartupNotification:  false,
		PlanConfirmation:             "smart",
		WorkspaceRaw:                 "workspace",
		SessionsPathRaw:              "sessions",
		MemoryPathRaw:                "memory",
		SkillsPathRaw:                "skills",
		SleepModeEnabled:             false,
		SleepModeInactivityThreshold: "30m",
		SleepModeResumeDelay:         "10m",
		SleepModeSubagentsEnabled:    false,
		WebSearchEnabled:             false,
		SearchProviders:              []string{"ddg"},
		BraveSearchAPIKey:            "",
		TavilyAPIKey:                 "",
	}
}

var checkOllamaModelsHook func(string) ([]string, error)

func CreateInteractive(path string, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)

	serverEnabled, err := askBool(reader, out, "Enable web server", true)
	if err != nil {
		return err
	}

	cfg := defaultInteractiveConfig("http://localhost:11434")
	cfg.ServerEnabled = serverEnabled

	baseURL, err := ask(reader, out, "Ollama base URL", "http://localhost:11434")
	if err != nil {
		return err
	}
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	cfg.OllamaBaseURL = normalized

	fmt.Fprintln(out, "Connecting to Ollama...")
	var defaultModel string
	var models []string
	var pingErr error
	if checkOllamaModelsHook != nil {
		models, pingErr = checkOllamaModelsHook(normalized)
	} else {
		models, pingErr = checkOllamaModels(normalized)
	}
	if pingErr != nil {
		fmt.Fprintf(out, "⚠️ Warning: Could not connect to Ollama at %s: %v\n", normalized, pingErr)
		fmt.Fprintln(out, "Make sure Ollama is running (run 'ollama serve' or open the Ollama desktop app).")
		continueAnyway, err := askBool(reader, out, "Continue with this URL anyway", true)
		if err != nil {
			return err
		}
		if !continueAnyway {
			return errors.New("setup aborted by user due to unreachable Ollama")
		}
		defaultModel, err = ask(reader, out, "Enter default chat model name to use", "")
		if err != nil {
			return err
		}
	} else if len(models) == 0 {
		fmt.Fprintln(out, "⚠️ No local models found in Ollama.")
		fmt.Fprintln(out, "To pull a model later, use 'ollama pull <model-name>' (e.g. 'ollama pull qwen2.5:7b').")
		defaultModel, err = ask(reader, out, "Enter default chat model name to use (leave empty to skip)", "")
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "\nAvailable Ollama models:")
		for i, m := range models {
			fmt.Fprintf(out, "  %d) %s\n", i+1, m)
		}
		idxStr, err := ask(reader, out, "Select default chat model (enter number or name)", "1")
		if err != nil {
			return err
		}
		idxStr = strings.TrimSpace(idxStr)
		if idx, err := strconv.Atoi(idxStr); err == nil && idx > 0 && idx <= len(models) {
			defaultModel = models[idx-1]
		} else if idxStr != "" {
			matched := false
			for _, m := range models {
				if strings.EqualFold(m, idxStr) {
					defaultModel = m
					matched = true
					break
				}
			}
			if !matched {
				defaultModel = idxStr
			}
		} else {
			defaultModel = models[0]
		}

		// Auto-detect embedding, vision, and image models from the list
		for _, m := range models {
			mLower := strings.ToLower(m)
			if cfg.OllamaModelEmbed == "" && (strings.Contains(mLower, "embed") || strings.Contains(mLower, "nomic") || strings.Contains(mLower, "bge")) {
				cfg.OllamaModelEmbed = m
				fmt.Fprintf(out, "Auto-assigned EMBED role to: %s\n", m)
			}
			if cfg.OllamaModelVision == "" && (strings.Contains(mLower, "vision") || strings.Contains(mLower, "vl") || strings.Contains(mLower, "llava") || strings.Contains(mLower, "minicpm")) {
				cfg.OllamaModelVision = m
				fmt.Fprintf(out, "Auto-assigned VISION role to: %s\n", m)
			}
			if cfg.OllamaModelImage == "" && (strings.Contains(mLower, "flux") || strings.Contains(mLower, "diffusion") || strings.Contains(mLower, "sdxl")) {
				cfg.OllamaModelImage = m
				fmt.Fprintf(out, "Auto-assigned IMAGE role to: %s\n", m)
			}
		}
	}

	cfg.OllamaDefaultModel = strings.TrimSpace(defaultModel)

	if serverEnabled {
		port, err := ask(reader, out, "Web server port", "8080")
		if err != nil {
			return err
		}
		webPort := strings.TrimPrefix(strings.TrimSpace(port), ":")
		if webPort == "" {
			webPort = "8080"
		}
		cfg.ServerPort = webPort

		exposeNetwork, err := askBool(reader, out, "Expose web server on the network (0.0.0.0)", false)
		if err != nil {
			return err
		}
		cfg.ServerExposeNetwork = exposeNetwork

		usePassword, err := askBool(reader, out, "Protect web server with a password", false)
		if err != nil {
			return err
		}
		if usePassword {
			password, err := ask(reader, out, "Web server password", "")
			if err != nil {
				return err
			}
			cfg.ServerPassword = password
		}

		token, err := ask(reader, out, "Telegram bot token (leave empty to skip)", "")
		if err != nil {
			return err
		}
		cfg.TelegramBotToken = strings.TrimSpace(token)
	} else {
		token, err := ask(reader, out, "Telegram bot token (leave empty to skip)", "")
		if err != nil {
			return err
		}
		cfg.TelegramBotToken = strings.TrimSpace(token)
	}

	if !cfg.ServerEnabled && cfg.TelegramBotToken == "" {
		fmt.Fprintln(out, "No channels configured. Saving .env with default values — edit the file to enable web or Telegram later.")
	}

	return SaveBasic(path, cfg)
}

type ollamaTag struct {
	Name string `json:"name"`
}
type ollamaTagsResponse struct {
	Models []ollamaTag `json:"models"`
}

func checkOllamaModels(baseURL string) ([]string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var res ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	var models []string
	for _, m := range res.Models {
		if m.Name != "" {
			models = append(models, m.Name)
		}
	}
	return models, nil
}

func SaveBasic(path string, cfg Config) error {
	workspacePath := cfg.WorkspaceRaw
	if workspacePath == "" {
		workspacePath = cfg.Workspace
	}
	sessionsPath := cfg.SessionsPathRaw
	if sessionsPath == "" {
		sessionsPath = cfg.SessionsPath
	}
	memoryPath := cfg.MemoryPathRaw
	if memoryPath == "" {
		memoryPath = cfg.MemoryPath
	}
	skillsPath := cfg.SkillsPathRaw
	if skillsPath == "" {
		skillsPath = cfg.SkillsPath
	}

	content := fmt.Sprintf(
		"# Server\n"+
			"SERVER_ENABLED=%t\n"+
			"SERVER_PORT=%s\n"+
			"SERVER_PASSWORD=%s\n"+
			"SERVER_EXPOSE_NETWORK=%t\n\n"+
			"# Sessions\n"+
			"SESSION_AUTO_NAME=%t\n"+
			"SESSION_EXPIRY_MIN=%d\n"+
			"SESSION_PLAN_CONFIRMATION=%s\n\n"+
			"# Resource Limits\n"+
			"SUBAGENT_TIMEOUT_MINUTES=%d\n\n"+
			"# Ollama\n"+
			"OLLAMA_BASE_URL=%s\n"+
			"OLLAMA_DEFAULT_MODEL=%s\n"+
			"OLLAMA_PROBE_MODELS=%s\n"+
			"OLLAMA_MODEL_VISION=%s\n"+
			"OLLAMA_MODEL_AUDIO=%s\n"+
			"OLLAMA_MODEL_EMBED=%s\n"+
			"OLLAMA_MODEL_IMAGE=%s\n"+
			"OLLAMA_MODEL_LEARNING=%s\n"+
			"OLLAMA_MODEL_SUBAGENT=%s\n"+
			"OLLAMA_IMAGE_STEPS=%d\n"+
			"OLLAMA_THINK_ENABLED=%t\n"+
			"OLLAMA_MAX_TOKENS=%d\n"+
			"OLLAMA_MAX_CONTEXT=%d\n\n"+
			"# Telegram\n"+
			"TELEGRAM_BOT_TOKEN=%s\n"+
			"TELEGRAM_AUTHORIZED_IDS=%s\n"+
			"TELEGRAM_STARTUP_NOTIFICATION=%t\n\n"+
			"# Paths\n"+
			"WORKSPACE_PATH=%s\n"+
			"SESSIONS_PATH=%s\n"+
			"MEMORY_PATH=%s\n"+
			"SKILLS_PATH=%s\n\n"+
			"# Sleep Mode\n"+
			"SLEEP_MODE_ENABLED=%t\n"+
			"SLEEP_MODE_INACTIVITY_THRESHOLD=%s\n"+
			"SLEEP_MODE_RESUME_DELAY=%s\n"+
			"SLEEP_MODE_SUBAGENTS_ENABLED=%t\n\n"+
			"# Web Search\n"+
			"WEB_SEARCH_ENABLED=%t\n"+
			"WEB_SEARCH_PRIORITY=%s\n"+
			"WEB_SEARCH_BRAVE_API_KEY=%s\n"+
			"WEB_SEARCH_TAVILY_API_KEY=%s\n",
		cfg.ServerEnabled,
		cfg.ServerPort,
		cfg.ServerPassword,
		cfg.ServerExposeNetwork,
		cfg.SessionAutoName,
		cfg.TelegramSessionExpiryMin,
		cfg.PlanConfirmation,
		cfg.SubagentTimeoutMinutes,
		cfg.OllamaBaseURL,
		cfg.OllamaDefaultModel,
		strings.Join(cfg.OllamaProbeModels, ","),
		cfg.OllamaModelVision,
		cfg.OllamaModelAudio,
		cfg.OllamaModelEmbed,
		cfg.OllamaModelImage,
		cfg.OllamaModelLearning,
		cfg.OllamaModelSubagent,
		cfg.OllamaImageSteps,
		cfg.OllamaThinkEnabled,
		cfg.OllamaMaxTokens,
		cfg.OllamaMaxContext,
		cfg.TelegramBotToken,
		strings.Join(cfg.TelegramAuthorizedIDs, ","),
		cfg.TelegramStartupNotification,
		workspacePath,
		sessionsPath,
		memoryPath,
		skillsPath,
		cfg.SleepModeEnabled,
		cfg.SleepModeInactivityThreshold,
		cfg.SleepModeResumeDelay,
		cfg.SleepModeSubagentsEnabled,
		cfg.WebSearchEnabled,
		strings.Join(cfg.SearchProviders, ","),
		cfg.BraveSearchAPIKey,
		cfg.TavilyAPIKey,
	)
	return os.WriteFile(path, []byte(content), 0o600)
}

func Parse(file *os.File) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("invalid .env line")
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, errors.New("invalid .env key")
		}
		values[key] = unquote(strings.TrimSpace(value))
	}
	return values, scanner.Err()
}

func ask(reader *bufio.Reader, out io.Writer, label, fallback string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, fallback)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func askBool(reader *bufio.Reader, out io.Writer, label string, fallback bool) (bool, error) {
	fallbackLabel := "y"
	if !fallback {
		fallbackLabel = "n"
	}
	answer, err := ask(reader, out, label+" (y/n)", fallbackLabel)
	if err != nil {
		return false, err
	}
	return parseBool(answer), nil
}

func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, "/")
	raw = strings.TrimSuffix(raw, "/api")
	if raw == "" {
		return "", errors.New("OLLAMA_BASE_URL is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("OLLAMA_BASE_URL must include scheme and host")
	}
	return parsed.String(), nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "s", "si", "sí", "on":
		return true
	default:
		return false
	}
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

// Manager manages a shared, thread-safe configuration instance.
type Manager struct {
	mu  sync.RWMutex
	cfg Config
}

// NewManager creates a new thread-safe configuration manager.
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// Get returns a copy of the current configuration.
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Set updates the configuration.
func (m *Manager) Set(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

// Update allows modifying fields in the configuration under a write lock.
func (m *Manager) Update(fn func(*Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.cfg)
}
