package learning

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
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

	sm := NewSleepManager(cfg, nil)
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

	sm := NewSleepManager(cfg, nil)
	
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

	sm := NewSleepManager(cfg, nil)

	// Test state save/load
	sm.state.AnalyzedSessions = []string{"sess-1", "sess-2"}
	err = sm.SaveState()
	if err != nil {
		t.Fatalf("save state failed: %v", err)
	}

	// Create another manager to load
	sm2 := NewSleepManager(cfg, nil)
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

	sm := NewSleepManager(cfg, nil)
	
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
