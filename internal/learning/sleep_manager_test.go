package learning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestNewSleepManager(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sleep-mgr-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Config{
		Workspace:                    filepath.Join(tmpDir, "workspace"),
		SessionsPath:                 filepath.Join(tmpDir, "sessions"),
		SkillsPath:                   filepath.Join(tmpDir, "skills"),
		SleepModeEnabled:             true,
		SleepModeInactivityThreshold: "5s",
	}

	sm := NewSleepManager(cfg, nil, nil)
	if sm == nil {
		t.Fatal("expected sleep manager, got nil")
	}

	if sm.isSleeping {
		t.Error("expected isSleeping to be false initially")
	}
}

func TestNotifyUserActivity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sleep-mgr-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Config{
		Workspace:                    filepath.Join(tmpDir, "workspace"),
		SessionsPath:                 filepath.Join(tmpDir, "sessions"),
		SkillsPath:                   filepath.Join(tmpDir, "skills"),
		SleepModeEnabled:             true,
		SleepModeInactivityThreshold: "5s",
	}

	sm := NewSleepManager(cfg, nil, nil)
	
	// Set initial past activity time
	past := time.Now().Add(-10 * time.Second)
	sm.lastActivity = past
	
	sm.NotifyUserActivity()

	if sm.lastActivity.Before(time.Now().Add(-1 * time.Second)) {
		t.Error("expected lastActivity to be updated to present time")
	}
}

func TestPauseAndState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sleep-mgr-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Config{
		Workspace:                    filepath.Join(tmpDir, "workspace"),
		SessionsPath:                 filepath.Join(tmpDir, "sessions"),
		SkillsPath:                   filepath.Join(tmpDir, "skills"),
		SleepModeEnabled:             true,
		SleepModeInactivityThreshold: "5s",
	}

	sm := NewSleepManager(cfg, nil, nil)

	// Test state save/load
	sm.state.AnalyzedSessions = []string{"sess-1", "sess-2"}
	err = sm.SaveState()
	if err != nil {
		t.Fatalf("save state failed: %v", err)
	}

	// Create another manager to load
	sm2 := NewSleepManager(cfg, nil, nil)
	sm2.LoadState()

	if len(sm2.state.AnalyzedSessions) != 2 || sm2.state.AnalyzedSessions[0] != "sess-1" {
		t.Errorf("expected loaded state to have sess-1, got %v", sm2.state.AnalyzedSessions)
	}

	// Test Pause Cancels context
	_, cancel := context.WithCancel(context.Background())
	sm.learnCancel = cancel
	sm.isSleeping = true
	sm.isLearning = true

	sm.Pause()

	if sm.isSleeping || sm.isLearning {
		t.Error("expected isSleeping and isLearning to be false after pause")
	}
	if sm.learnCancel != nil {
		t.Error("expected learnCancel to be nil after pause")
	}
}

func TestAppendToAuditLog(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sleep-mgr-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	_ = os.MkdirAll(skillsDir, 0755)

	cfg := config.Config{
		Workspace:                    filepath.Join(tmpDir, "workspace"),
		SessionsPath:                 filepath.Join(tmpDir, "sessions"),
		SkillsPath:                   skillsDir,
		SleepModeEnabled:             true,
		SleepModeInactivityThreshold: "5s",
	}

	sm := NewSleepManager(cfg, nil, nil)
	
	sm.appendToAuditLog("sess-abc", []string{"Created skill X", "Edited skill Y"}, "Made improvements")

	auditPath := filepath.Join(skillsDir, "audit_log.md")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Skills Continuous Learning") || !strings.Contains(content, "sess-abc") {
		t.Errorf("Audit log did not contain expected content. Got:\n%s", content)
	}
}

func TestCheckHardwareAndSelectModel(t *testing.T) {
	var psModels []ollama.RunningModel
	var psErr error

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ps" {
			if psErr != nil {
				http.Error(w, psErr.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollama.PsResponse{Models: psModels})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)

	cfg := config.Config{
		OllamaDefaultModel:   "default-model:latest",
		OllamaModelLearning:  "learning-model",
		OllamaModelSubagent:  "subagent-model:1b",
	}

	sm := NewSleepManager(cfg, client, nil)

	// Case 1: No models loaded. Should return primary preferred model (subagent-model:1b).
	psModels = nil
	psErr = nil
	model, err := sm.checkHardwareAndSelectModel(context.Background())
	if err != nil {
		t.Fatalf("Case 1 failed: %v", err)
	}
	if model != "subagent-model:1b" {
		t.Errorf("Case 1: expected subagent-model:1b, got %q", model)
	}

	// Case 2: Subagent model is loaded (with tag normalization). Should return subagent-model:1b.
	psModels = []ollama.RunningModel{
		{Name: "registry.ollama.ai/library/subagent-model:1b"},
	}
	model, err = sm.checkHardwareAndSelectModel(context.Background())
	if err != nil {
		t.Fatalf("Case 2 failed: %v", err)
	}
	if model != "subagent-model:1b" {
		t.Errorf("Case 2: expected subagent-model:1b, got %q", model)
	}

	// Case 3: Only learning model is loaded. Should return learning-model.
	psModels = []ollama.RunningModel{
		{Name: "learning-model:latest"},
	}
	model, err = sm.checkHardwareAndSelectModel(context.Background())
	if err != nil {
		t.Fatalf("Case 3 failed: %v", err)
	}
	if model != "learning-model" {
		t.Errorf("Case 3: expected learning-model, got %q", model)
	}

	// Case 4: Only default model is loaded. Should return default-model:latest.
	psModels = []ollama.RunningModel{
		{Name: "default-model"},
	}
	model, err = sm.checkHardwareAndSelectModel(context.Background())
	if err != nil {
		t.Fatalf("Case 4 failed: %v", err)
	}
	if model != "default-model:latest" {
		t.Errorf("Case 4: expected default-model:latest, got %q", model)
	}

	// Case 5: A completely different model is loaded. Should return error (defer).
	psModels = []ollama.RunningModel{
		{Name: "some-other-model:7b"},
	}
	model, err = sm.checkHardwareAndSelectModel(context.Background())
	if err == nil {
		t.Error("Case 5: expected error (defer), got nil")
	}
	if model != "" {
		t.Errorf("Case 5: expected empty model name, got %q", model)
	}
}

