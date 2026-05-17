package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	OllamaBaseURL      string
	OllamaProbeModels  []string
	OllamaDefaultModel string
	TelegramBotToken   string
	WebAddr            string
	WebEnabled         bool
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
		OllamaBaseURL: "http://localhost:11434",
		WebAddr:       ":8080",
		WebEnabled:    true,
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
	cfg.TelegramBotToken = apply("TELEGRAM_BOT_TOKEN")
	if value := apply("WEB_ENABLED"); value != "" {
		cfg.WebEnabled = parseBool(value)
	}
	if value := apply("WEB_ADDR"); value != "" {
		cfg.WebAddr = value
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
	webAnswer, err := ask(reader, out, "Levantar servidor web? (s/n)", "s")
	if err != nil {
		return err
	}
	port, err := ask(reader, out, "Puerto web", "8080")
	if err != nil {
		return err
	}
	webEnabled := parseBool(webAnswer)
	webAddr := ":" + strings.TrimPrefix(strings.TrimSpace(port), ":")
	content := fmt.Sprintf("OLLAMA_BASE_URL=%s\nWEB_ENABLED=%t\nWEB_ADDR=%s\nOLLAMA_PROBE_MODELS=\nOLLAMA_DEFAULT_MODEL=\nTELEGRAM_BOT_TOKEN=\n", baseURL, webEnabled, webAddr)
	return os.WriteFile(path, []byte(content), 0o600)
}

func SaveBasic(path string, cfg Config) error {
	content := fmt.Sprintf("OLLAMA_BASE_URL=%s\nWEB_ENABLED=%t\nWEB_ADDR=%s\nOLLAMA_PROBE_MODELS=%s\nOLLAMA_DEFAULT_MODEL=%s\nTELEGRAM_BOT_TOKEN=%s\n",
		cfg.OllamaBaseURL,
		cfg.WebEnabled,
		cfg.WebAddr,
		strings.Join(cfg.OllamaProbeModels, ","),
		cfg.OllamaDefaultModel,
		cfg.TelegramBotToken,
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
