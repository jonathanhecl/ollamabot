package cache

import (
	"encoding/json"
	"os"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type Snapshot struct {
	GeneratedAt   time.Time                  `json:"generated_at"`
	BaseURL       string                     `json:"base_url"`
	OllamaVersion string                     `json:"ollama_version"`
	Models        []capabilities.ModelReport `json:"models"`
	Running       []ollama.RunningModel      `json:"running"`
	Expected      []ExpectedProbe            `json:"expected"`
}

type ExpectedProbe struct {
	Name    string              `json:"name"`
	Model   string              `json:"model"`
	Status  capabilities.Status `json:"status"`
	Details string              `json:"details"`
}

func Save(path string, snapshot Snapshot) error {
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func Load(path string) (Snapshot, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
