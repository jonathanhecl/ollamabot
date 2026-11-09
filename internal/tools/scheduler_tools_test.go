package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type mockSchedulerService struct {
	tasks []ScheduledTaskInfo
}

func (m *mockSchedulerService) CreateScheduledTask(spec ScheduledTaskSpec) (ScheduledTaskInfo, error) {
	task := ScheduledTaskInfo{
		ID:             "rem_test123",
		Type:           spec.Type,
		Title:          spec.Title,
		Prompt:         spec.Prompt,
		Channel:        spec.Channel,
		NotifyChannels: spec.NotifyChannels,
		Status:         "pending",
	}
	if spec.Type == "agent_task" {
		task.ID = "task_test456"
		task.ScheduleType = "cron"
		task.CronExpr = "0 9 * * *"
		task.NextRunAt = time.Now().Add(1 * time.Hour)
	} else {
		task.ScheduleType = "once"
		task.NextRunAt = time.Now().Add(15 * time.Minute)
	}
	m.tasks = append(m.tasks, task)
	return task, nil
}

func (m *mockSchedulerService) UpdateScheduledTask(id string, patch ScheduledTaskPatch) (ScheduledTaskInfo, error) {
	for i := range m.tasks {
		if m.tasks[i].ID != id {
			continue
		}
		if patch.Title != "" {
			m.tasks[i].Title = patch.Title
		}
		if patch.Prompt != "" {
			m.tasks[i].Prompt = patch.Prompt
		}
		if patch.NotifyChannels != nil {
			m.tasks[i].NotifyChannels = patch.NotifyChannels
		}
		if patch.Status != "" {
			m.tasks[i].Status = patch.Status
		}
		return m.tasks[i], nil
	}
	return ScheduledTaskInfo{}, context.Canceled
}

func (m *mockSchedulerService) DeleteScheduledTask(id string) error {
	var remaining []ScheduledTaskInfo
	for _, t := range m.tasks {
		if t.ID != id {
			remaining = append(remaining, t)
		}
	}
	m.tasks = remaining
	return nil
}

func (m *mockSchedulerService) ListTasks(channel string, sessionID string, includeCompleted bool) []ScheduledTaskInfo {
	return m.tasks
}

func (m *mockSchedulerService) CancelTask(id string) error {
	return m.DeleteScheduledTask(id)
}

func TestSchedulerTools(t *testing.T) {
	mock := &mockSchedulerService{}

	r := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	r.SetSchedulerService(mock)
	r.SetChannel("telegram")
	r.SetTargetChatID(98765)

	// Test schedule_reminder
	res, err := r.execute(context.Background(), "schedule_reminder", map[string]any{
		"text": "Call doctor",
		"when": "in 15m",
	})
	if err != nil {
		t.Fatalf("schedule_reminder failed: %v", err)
	}
	if !strings.Contains(res, "Reminder scheduled successfully!") {
		t.Errorf("unexpected output: %s", res)
	}

	// Test schedule_list
	listRes, err := r.execute(context.Background(), "schedule_list", map[string]any{})
	if err != nil {
		t.Fatalf("schedule_list failed: %v", err)
	}
	if !strings.Contains(listRes, "Call doctor") {
		t.Errorf("expected list to contain 'Call doctor', got: %s", listRes)
	}

	tasks := mock.ListTasks("telegram", "", false)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task in manager, got %d", len(tasks))
	}
	taskID := tasks[0].ID

	// Test schedule_update
	updateRes, err := r.execute(context.Background(), "schedule_update", map[string]any{
		"task_id": taskID,
		"title":   "Call the doctor",
		"status":  "paused",
	})
	if err != nil {
		t.Fatalf("schedule_update failed: %v", err)
	}
	if !strings.Contains(updateRes, "updated") {
		t.Errorf("unexpected update output: %s", updateRes)
	}
	updated := mock.ListTasks("", "", false)[0]
	if updated.Title != "Call the doctor" || updated.Status != "paused" {
		t.Errorf("update not applied: %+v", updated)
	}

	// Test schedule_delete
	deleteRes, err := r.execute(context.Background(), "schedule_delete", map[string]any{
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("schedule_delete failed: %v", err)
	}
	if !strings.Contains(deleteRes, "deleted") {
		t.Errorf("unexpected delete output: %s", deleteRes)
	}

	// List again - should be empty
	listEmpty, err := r.execute(context.Background(), "schedule_list", map[string]any{})
	if err != nil {
		t.Fatalf("schedule_list failed: %v", err)
	}
	if !strings.Contains(listEmpty, "No active reminders") {
		t.Errorf("expected empty message, got: %s", listEmpty)
	}
}

func TestScheduleCreationRequiresApproval(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSchedulerService(&mockSchedulerService{})
	registry.SetApprovalHandler(denyApprovalHandler{})

	args, _ := json.Marshal(map[string]any{
		"instruction": "Check bitcoin",
		"when":        "0 9 * * *",
	})
	got, err := registry.Execute(context.Background(), ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "schedule_task",
			Arguments: args,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "denied") {
		t.Fatalf("expected the creation to be denied by the approval handler, got %q", got)
	}
}

type denyApprovalHandler struct{}

func (denyApprovalHandler) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	return false, nil
}
