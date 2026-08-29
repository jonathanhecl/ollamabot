package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
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
