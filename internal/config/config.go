package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	SessionsPath                 string
	MemoryPath                   string
	SkillsPath                   string
	SleepModeEnabled             bool
	SleepModeInactivityThreshold string
	SleepModeResumeDelay         string
	OllamaModelLearning          string
	SearchProviders              []string // ordered list: "brave", "tavily", "ddg"
	BraveSearchAPIKey            string
	TavilyAPIKey                 string
	SleepModeSubagentsEnabled    bool
	OllamaModelSubagent          string
	ServerPassword               string
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
		SessionsPath:                 "sessions",
		MemoryPath:                   "memory",
		SkillsPath:                   "skills",
		SleepModeEnabled:             false,
		SleepModeInactivityThreshold: "30m",
		SleepModeResumeDelay:         "10m",
		OllamaModelLearning:          "",
		SearchProviders:              []string{"ddg"},
		BraveSearchAPIKey:            "",
		TavilyAPIKey:                 "",
		SleepModeSubagentsEnabled:    false,
		OllamaModelSubagent:          "",
		ServerPassword:               "",
		TelegramSessionExpiryMin:     30,
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
	if value := apply("TELEGRAM_SESSION_EXPIRY_MIN"); value != "" {
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
	}
	if value := apply("SESSIONS_PATH"); value != "" {
		cfg.SessionsPath = value
	}
	if value := apply("MEMORY_PATH"); value != "" {
		cfg.MemoryPath = value
	}
	if value := apply("SKILLS_PATH"); value != "" {
		cfg.SkillsPath = value
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
	if value := apply("SERVER_PASSWORD"); value != "" {
		cfg.ServerPassword = value
	}
	if value := apply("SEARCH_PROVIDERS"); value != "" {
		cfg.SearchProviders = splitCSV(value)
	}
	if value := apply("BRAVE_SEARCH_API_KEY"); value != "" {
		cfg.BraveSearchAPIKey = value
	}
	if value := apply("TAVILY_API_KEY"); value != "" {
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

func CreateInteractive(path string, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	baseURL, err := ask(reader, out, "Ollama URL", "http://localhost:11434")
	if err != nil {
		return err
	}
	webAnswer, err := ask(reader, out, "Enable server? (y/n)", "y")
	if err != nil {
		return err
	}
	port, err := ask(reader, out, "Server port", "8080")
	if err != nil {
		return err
	}
	serverEnabled := parseBool(webAnswer)
	webPort := strings.TrimPrefix(strings.TrimSpace(port), ":")
	if webPort == "" {
		webPort = "8080"
	}
	content := fmt.Sprintf("OLLAMA_BASE_URL=%s\nSERVER_ENABLED=%t\nSERVER_PORT=%s\nWEB_SEARCH_ENABLED=false\nSERVER_EXPOSE_NETWORK=true\nOLLAMA_PROBE_MODELS=\nOLLAMA_DEFAULT_MODEL=\nTELEGRAM_BOT_TOKEN=\nTELEGRAM_AUTHORIZED_IDS=\nTELEGRAM_SESSION_EXPIRY_MIN=30\nWORKSPACE_PATH=workspace\nSESSIONS_PATH=sessions\nMEMORY_PATH=memory\nSKILLS_PATH=skills\n", baseURL, serverEnabled, webPort)
	return os.WriteFile(path, []byte(content), 0o600)
}

func SaveBasic(path string, cfg Config) error {
	content := fmt.Sprintf(
		"OLLAMA_BASE_URL=%s\nSERVER_ENABLED=%t\nSERVER_PORT=%s\nWEB_SEARCH_ENABLED=%t\nSERVER_EXPOSE_NETWORK=%t\nSESSION_AUTO_NAME=%t\nOLLAMA_PROBE_MODELS=%s\nOLLAMA_DEFAULT_MODEL=%s\nOLLAMA_MODEL_VISION=%s\nOLLAMA_MODEL_AUDIO=%s\nOLLAMA_MODEL_EMBED=%s\nOLLAMA_MODEL_IMAGE=%s\nOLLAMA_IMAGE_STEPS=%d\nTELEGRAM_BOT_TOKEN=%s\nTELEGRAM_AUTHORIZED_IDS=%s\nTELEGRAM_SESSION_EXPIRY_MIN=%d\nTELEGRAM_STARTUP_NOTIFICATION=%t\nWORKSPACE_PATH=%s\nSESSIONS_PATH=%s\nMEMORY_PATH=%s\nSKILLS_PATH=%s\nSLEEP_MODE_ENABLED=%t\nSLEEP_MODE_INACTIVITY_THRESHOLD=%s\nSLEEP_MODE_RESUME_DELAY=%s\nOLLAMA_MODEL_LEARNING=%s\nSEARCH_PROVIDERS=%s\nBRAVE_SEARCH_API_KEY=%s\nTAVILY_API_KEY=%s\nSLEEP_MODE_SUBAGENTS_ENABLED=%t\nOLLAMA_MODEL_SUBAGENT=%s\nSERVER_PASSWORD=%s\n",
		cfg.OllamaBaseURL,
		cfg.ServerEnabled,
		cfg.ServerPort,
		cfg.WebSearchEnabled,
		cfg.ServerExposeNetwork,
		cfg.SessionAutoName,
		strings.Join(cfg.OllamaProbeModels, ","),
		cfg.OllamaDefaultModel,
		cfg.OllamaModelVision,
		cfg.OllamaModelAudio,
		cfg.OllamaModelEmbed,
		cfg.OllamaModelImage,
		cfg.OllamaImageSteps,
		cfg.TelegramBotToken,
		strings.Join(cfg.TelegramAuthorizedIDs, ","),
		cfg.TelegramSessionExpiryMin,
		cfg.TelegramStartupNotification,
		cfg.Workspace,
		cfg.SessionsPath,
		cfg.MemoryPath,
		cfg.SkillsPath,
		cfg.SleepModeEnabled,
		cfg.SleepModeInactivityThreshold,
		cfg.SleepModeResumeDelay,
		cfg.OllamaModelLearning,
		strings.Join(cfg.SearchProviders, ","),
		cfg.BraveSearchAPIKey,
		cfg.TavilyAPIKey,
		cfg.SleepModeSubagentsEnabled,
		cfg.OllamaModelSubagent,
		cfg.ServerPassword,
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
