package config

import (
	"bufio"
	"errors"
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

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
