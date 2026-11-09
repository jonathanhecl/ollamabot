package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

func TestManager_CreateAndTickAlert(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sched_test_*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := config.Config{
		SessionsPath: filepath.Join(tempDir, "sessions"),
		MemoryPath:   filepath.Join(tempDir, "memory"),
		Workspace:    filepath.Join(tempDir, "workspace"),
	}
	_ = os.MkdirAll(cfg.SessionsPath, 0755)
	_ = os.MkdirAll(cfg.MemoryPath, 0755)
	_ = os.MkdirAll(cfg.Workspace, 0755)

	cfgMgr := config.NewManager(cfg)
	mgr := NewManager(cfgMgr, nil, nil, nil)

	notifiedChan := make(chan string, 1)
	mgr.RegisterNotifier("telegram", func(task Task, message string) error {
		notifiedChan <- message
		return nil
	})

	// Create a reminder for right now (or 10ms ago)
	task, err := mgr.CreateReminder("telegram", "sess_1", 12345, "Take out the trash", "in 1s")
	if err != nil {
		t.Fatalf("failed to create reminder: %v", err)
	}
	if task.ID == "" {
		t.Errorf("expected task ID to be populated")
	}

	// Manually set NextRunAt to now-1s to trigger immediately
	mgr.mu.Lock()
	tItem := mgr.tasks[task.ID]
	tItem.NextRunAt = time.Now().Add(-1 * time.Second)
	mgr.tasks[task.ID] = tItem
	mgr.mu.Unlock()

	// Run tick
	mgr.Tick(context.Background())

	select {
	case msg := <-notifiedChan:
		if msg != "⏰ **Reminder**: Take out the trash" {
			t.Errorf("unexpected message: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for notification")
	}

	// Check task completed
	savedTask, err := mgr.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if savedTask.Status != TaskStatusCompleted {
		t.Errorf("expected status completed, got %v", savedTask.Status)
	}
	if savedTask.RunCount != 1 {
		t.Errorf("expected RunCount 1, got %d", savedTask.RunCount)
	}
}

func TestManager_CancelTask(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sched_cancel_test_*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := config.Config{
		SessionsPath: filepath.Join(tempDir, "sessions"),
		MemoryPath:   filepath.Join(tempDir, "memory"),
	}
	_ = os.MkdirAll(cfg.SessionsPath, 0755)
	cfgMgr := config.NewManager(cfg)
	mgr := NewManager(cfgMgr, nil, nil, nil)

	task, err := mgr.CreateReminder("telegram", "", 123, "Check something", "in 1h")
	if err != nil {
		t.Fatalf("create reminder failed: %v", err)
	}

	if err := mgr.CancelTask(task.ID); err != nil {
		t.Fatalf("cancel task failed: %v", err)
	}

	tUpdated, _ := mgr.GetTask(task.ID)
	if tUpdated.Status != TaskStatusCancelled {
		t.Errorf("expected cancelled status, got %v", tUpdated.Status)
	}

	activeTasks := mgr.ListTasks("telegram", "", false)
	if len(activeTasks) != 0 {
		t.Errorf("expected 0 active tasks, got %d", len(activeTasks))
	}
}

func newTestManager(t *testing.T, cfg config.Config) *Manager {
	t.Helper()
	if cfg.SessionsPath == "" {
		cfg.SessionsPath = filepath.Join(t.TempDir(), "sessions")
	}
	if cfg.MemoryPath == "" {
		cfg.MemoryPath = filepath.Join(t.TempDir(), "memory")
	}
	if err := os.MkdirAll(cfg.SessionsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	return NewManager(config.NewManager(cfg), nil, nil, nil)
}

func TestManager_UpdatePauseResumeDelete(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:        "web",
		Title:          "Bitcoin check",
		Type:           "agent_task",
		Prompt:         "Tell me how bitcoin is doing",
		When:           "0 9 * * *",
		NotifyChannels: []string{"web", "telegram"},
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err = mgr.UpdateScheduledTask(info.ID, tools.ScheduledTaskPatch{Status: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "paused" {
		t.Fatalf("status = %q, want paused", info.Status)
	}

	info, err = mgr.UpdateScheduledTask(info.ID, tools.ScheduledTaskPatch{Status: "pending", Title: "BTC morning"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "pending" || info.Title != "BTC morning" {
		t.Fatalf("unexpected update: %+v", info)
	}

	if err := mgr.DeleteScheduledTask(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.GetTask(info.ID); err == nil {
		t.Fatal("expected deleted task to be gone")
	}
}

func TestManager_CreatesWebSessionForTask(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Title:   "Daily report",
		Type:    "agent_task",
		Prompt:  "Summarize the news",
		When:    "0 8 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID == "" {
		t.Fatal("expected a dedicated session for a web-delivered task")
	}
	sess, err := mgr.sessionStore.Get(info.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sess.Title, "⏰ ") {
		t.Fatalf("unexpected session title %q", sess.Title)
	}
}

func TestManager_FansOutNotifications(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	webCh := make(chan string, 1)
	tgCh := make(chan string, 1)
	mgr.RegisterNotifier("web", func(task Task, message string) error {
		webCh <- message
		return nil
	})
	mgr.RegisterNotifier("telegram", func(task Task, message string) error {
		tgCh <- message
		return nil
	})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:        "web",
		Prompt:         "Drink water",
		When:           "in 1s",
		NotifyChannels: []string{"web", "telegram"},
	})
	if err != nil {
		t.Fatal(err)
	}

	mgr.mu.Lock()
	task := mgr.tasks[info.ID]
	task.NextRunAt = time.Now().Add(-time.Second)
	mgr.tasks[info.ID] = task
	mgr.mu.Unlock()

	mgr.Tick(context.Background())

	for name, ch := range map[string]chan string{"web": webCh, "telegram": tgCh} {
		select {
		case msg := <-ch:
			if !strings.Contains(msg, "Drink water") {
				t.Errorf("%s got unexpected message %q", name, msg)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("timed out waiting for %s notification", name)
		}
	}
}

func TestManager_MigratesLegacyTask(t *testing.T) {
	cfg := config.Config{SessionsPath: filepath.Join(t.TempDir(), "sessions")}
	if err := os.MkdirAll(cfg.SessionsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []Task{{
		ID:           "rem_legacy",
		Type:         TaskTypeAgentTask,
		ScheduleType: ScheduleTypeCron,
		Prompt:       "Legacy task",
		CronExpr:     "0 9 * * *",
		Channel:      "web",
		SessionID:    "old_chat_session",
		CreatedAt:    time.Now(),
		NextRunAt:    time.Now().Add(time.Hour),
		Status:       TaskStatusPending,
	}}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(cfg.SessionsPath, "scheduler_tasks.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(config.NewManager(cfg), nil, nil, nil)
	task, err := mgr.GetTask("rem_legacy")
	if err != nil {
		t.Fatal(err)
	}
	if task.OriginSessionID != "old_chat_session" || task.SessionID != "" {
		t.Fatalf("legacy migration failed: %+v", task)
	}
	if len(task.NotifyChannels) == 0 {
		t.Fatal("expected default notify channels after migration")
	}
}
