package cache

import (
	"encoding/json"
	"os"
	"strings"
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
	ProbeRuns     []ProbeRun                 `json:"probe_runs,omitempty"`
}

type ExpectedProbe struct {
	Name    string              `json:"name"`
	Model   string              `json:"model"`
	Status  capabilities.Status `json:"status"`
	Details string              `json:"details"`
}

type ProbeRun struct {
	Name      string              `json:"name"`
	Model     string              `json:"model"`
	Status    capabilities.Status `json:"status"`
	Details   string              `json:"details"`
	RunAt     time.Time           `json:"run_at"`
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

// Checker returns a capability lookup function backed by the snapshot at path.
// A model is considered to have a capability when its probed status is
// Confirmed or Inferred. Returns nil when the snapshot cannot be loaded so
// callers can fall back to heuristics.
func Checker(path string) func(model, capability string) bool {
	snapshot, err := Load(path)
	if err != nil || len(snapshot.Models) == 0 {
		return nil
	}
	caps := make(map[string]map[string]capabilities.Status, len(snapshot.Models))
	for _, report := range snapshot.Models {
		caps[report.Name] = report.Capabilities
	}
	lookup := func(model string) map[string]capabilities.Status {
		if c, ok := caps[model]; ok {
			return c
		}
		// Tolerate the implicit ":latest" tag in either direction.
		if c, ok := caps[model+":latest"]; ok {
			return c
		}
		if c, ok := caps[strings.TrimSuffix(model, ":latest")]; ok {
			return c
		}
		return nil
	}
	return func(model, capability string) bool {
		statuses := lookup(strings.TrimSpace(model))
		if statuses == nil {
			// Unknown model (not probed yet): trust the configuration.
			return true
		}
		status := statuses[capability]
		return status == capabilities.Confirmed || status == capabilities.Inferred
	}
}

// SaveProbeRun loads the snapshot at path (if it exists), upserts the probe run
// (keyed by name+model, keeping the most recent), and saves back. If the file
// does not exist yet the run is saved in a minimal snapshot.
func SaveProbeRun(path string, run ProbeRun) error {
	snapshot, err := Load(path)
	if err != nil {
		snapshot = Snapshot{}
	}
	upserted := false
	for i, existing := range snapshot.ProbeRuns {
		if existing.Name == run.Name && existing.Model == run.Model {
			snapshot.ProbeRuns[i] = run
			upserted = true
			break
		}
	}
	if !upserted {
		snapshot.ProbeRuns = append(snapshot.ProbeRuns, run)
	}
	return Save(path, snapshot)
}
