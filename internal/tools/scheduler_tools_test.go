package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

type mockSchedulerService struct {
	tasks []ScheduledTaskInfo
}

func (m *mockSchedulerService) CreateReminder(channel string, sessionID string, targetChatID int64, text string, when string) (ScheduledTaskInfo, error) {
	task := ScheduledTaskInfo{
		ID:           "rem_test123",
		Type:         "alert",
		ScheduleType: "once",
		Prompt:       text,
		NextRunAt:    time.Now().Add(15 * time.Minute),
		Status:       "pending",
	}
	m.tasks = append(m.tasks, task)
	return task, nil
}

func (m *mockSchedulerService) CreateTask(channel string, sessionID string, targetChatID int64, instruction string, when string, isAgentTask bool) (ScheduledTaskInfo, error) {
	task := ScheduledTaskInfo{
		ID:           "task_test456",
		Type:         "agent_task",
		ScheduleType: "cron",
		Prompt:       instruction,
		CronExpr:     "0 9 * * *",
		NextRunAt:    time.Now().Add(1 * time.Hour),
		Status:       "pending",
	}
	m.tasks = append(m.tasks, task)
	return task, nil
}

func (m *mockSchedulerService) ListTasks(channel string, sessionID string, includeCompleted bool) []ScheduledTaskInfo {
	return m.tasks
}

func (m *mockSchedulerService) CancelTask(id string) error {
	var remaining []ScheduledTaskInfo
	for _, t := range m.tasks {
		if t.ID != id {
			remaining = append(remaining, t)
		}
	}
	m.tasks = remaining
	return nil
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

	// Test schedule_cancel
	cancelRes, err := r.execute(context.Background(), "schedule_cancel", map[string]any{
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("schedule_cancel failed: %v", err)
	}
	if !strings.Contains(cancelRes, "successfully cancelled") {
		t.Errorf("unexpected cancel output: %s", cancelRes)
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
