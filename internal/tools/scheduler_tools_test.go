package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type mockSchedulerService struct {
	tasks  []ScheduledTaskInfo
	manual *mockManualRun
}

// mockManualRun scripts the outcome of a forced run.
type mockManualRun struct {
	outcome ManualRunOutcome
	hold    bool // never deliver the outcome: the run keeps going
}

func (m *mockSchedulerService) CreateScheduledTask(spec ScheduledTaskSpec) (ScheduledTaskInfo, error) {
	task := ScheduledTaskInfo{
		ID:             "rem_test123",
		Type:           spec.Type,
		Title:          spec.Title,
		Prompt:         spec.Prompt,
		Channel:        spec.Channel,
		NotifyChannels: spec.NotifyChannels,
		NotifyMode:     spec.NotifyMode,
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

func (m *mockSchedulerService) RelatedTasks(query string, queryEmbedding []float64, limit int) []ScheduledTaskInfo {
	if limit <= 0 || limit > len(m.tasks) {
		limit = len(m.tasks)
	}
	return m.tasks[:limit]
}

func (m *mockSchedulerService) CancelTask(id string) error {
	return m.DeleteScheduledTask(id)
}

func (m *mockSchedulerService) RunScheduledTaskNow(id string) (<-chan ManualRunOutcome, ScheduledTaskInfo, error) {
	var info ScheduledTaskInfo
	found := false
	for _, t := range m.tasks {
		if t.ID == id {
			info = t
			found = true
			break
		}
	}
	if !found {
		return nil, ScheduledTaskInfo{}, fmt.Errorf("task %q not found", id)
	}

	out := make(chan ManualRunOutcome, 1)
	hold := m.manual != nil && m.manual.hold
	switch {
	case hold:
		// The run never delivers: the caller has to time out.
	case m.manual != nil:
		outcome := m.manual.outcome
		if outcome.Info.ID == "" {
			outcome.Info = info
		}
		out <- outcome
	default:
		out <- ManualRunOutcome{Info: info, Answer: "The run produced an answer."}
	}
	if !hold {
		close(out)
	}
	return out, info, nil
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

func TestScheduleTaskPassesNotifyMode(t *testing.T) {
	mock := &mockSchedulerService{}
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSchedulerService(mock)

	if _, err := registry.execute(context.Background(), "schedule_task", map[string]any{
		"instruction":     "Check for news about X",
		"when":            "every 5h",
		"notify_mode":     "on_alert",
		"notify_channels": []any{"web"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(mock.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(mock.tasks))
	}
	if mock.tasks[0].NotifyMode != "on_alert" {
		t.Fatalf("notify mode = %q, want on_alert", mock.tasks[0].NotifyMode)
	}
}

func TestSendScheduledAlertTool(t *testing.T) {
	var alerts []string
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetScheduledAlertHandler(func(message string) {
		alerts = append(alerts, message)
	})

	result, err := registry.execute(context.Background(), "send_scheduled_alert", map[string]any{
		"message": "New development in the story",
	})
	if err != nil {
		t.Fatalf("send_scheduled_alert failed: %v", err)
	}
	if len(alerts) != 1 || alerts[0] != "New development in the story" {
		t.Fatalf("unexpected alerts: %#v (result %q)", alerts, result)
	}

	disabled := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	if _, err := disabled.execute(context.Background(), "send_scheduled_alert", map[string]any{"message": "x"}); err == nil {
		t.Fatal("expected an error when no alert handler is configured")
	}
}

func TestRelatedScheduledTasksContext(t *testing.T) {
	lastRun := time.Date(2026, 11, 9, 14, 30, 0, 0, time.UTC)
	mock := &mockSchedulerService{tasks: []ScheduledTaskInfo{{
		ID:              "task_btc",
		Title:           "Bitcoin price",
		Type:            "agent_task",
		Prompt:          "Check the BTC price every 2h and alert above 100k",
		LastObservation: "BTC is at 67,100 USD",
		LastRunAt:       &lastRun,
	}}}
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSchedulerService(mock)

	block := registry.RelatedScheduledTasksContext("how is bitcoin doing?", nil, 3)
	for _, want := range []string{"Related Scheduled Task Observations", "Bitcoin price", "67,100", "measured 2026-11-09 14:30", "condition"} {
		if !strings.Contains(block, want) {
			t.Fatalf("related task block missing %q:\n%s", want, block)
		}
	}
}

type denyApprovalHandler struct{}

func (denyApprovalHandler) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	return false, nil
}

func newSchedulerRunRegistry(t *testing.T, mock *mockSchedulerService) *Registry {
	t.Helper()
	r := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	r.SetSchedulerService(mock)
	return r
}

func TestScheduleRunReportsTheRealOutcome(t *testing.T) {
	mock := &mockSchedulerService{
		tasks: []ScheduledTaskInfo{{
			ID:             "task_news",
			Type:           "agent_task",
			Title:          "News watch",
			Prompt:         "Check for news about X",
			NotifyChannels: []string{"web", "telegram"},
			NotifyMode:     "always",
			SessionID:      "sess_run",
			Status:         "pending",
			NextRunAt:      time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local),
		}},
		manual: &mockManualRun{outcome: ManualRunOutcome{
			Answer:   "Nothing major happened today.",
			Duration: 12 * time.Second,
		}},
	}
	r := newSchedulerRunRegistry(t, mock)

	res, err := r.execute(context.Background(), "schedule_run", map[string]any{"task_id": "task_news"})
	if err != nil {
		t.Fatalf("schedule_run failed: %v", err)
	}
	for _, want := range []string{"finished in 12s", "Nothing major happened today.", "sess_run", "web, telegram", "next run: 2026-11-16 09:00", "schedule was not changed"} {
		if !strings.Contains(res, want) {
			t.Errorf("output missing %q:\n%s", want, res)
		}
	}
}

func TestScheduleRunExplainsSilentRunWithoutAlert(t *testing.T) {
	mock := &mockSchedulerService{
		tasks: []ScheduledTaskInfo{{
			ID:             "task_silent",
			Type:           "agent_task",
			Title:          "Silent watch",
			Prompt:         "Only tell me if something breaks",
			NotifyChannels: []string{"web"},
			NotifyMode:     "on_alert",
			Status:         "pending",
		}},
		manual: &mockManualRun{outcome: ManualRunOutcome{Answer: "All quiet."}},
	}
	r := newSchedulerRunRegistry(t, mock)

	res, err := r.execute(context.Background(), "schedule_run", map[string]any{"task_id": "task_silent"})
	if err != nil {
		t.Fatalf("schedule_run failed: %v", err)
	}
	if !strings.Contains(res, "would NOT have received a message") {
		t.Errorf("silent run must state that nothing was delivered:\n%s", res)
	}
	if !strings.Contains(res, "All quiet.") {
		t.Errorf("output should include the run report:\n%s", res)
	}
}

func TestScheduleRunReportsFailure(t *testing.T) {
	mock := &mockSchedulerService{
		tasks: []ScheduledTaskInfo{{
			ID:             "task_broken",
			Type:           "agent_task",
			Title:          "Broken task",
			Prompt:         "Do something",
			NotifyChannels: []string{"web"},
			Status:         "pending",
		}},
		manual: &mockManualRun{outcome: ManualRunOutcome{Err: "no default model configured"}},
	}
	r := newSchedulerRunRegistry(t, mock)

	res, err := r.execute(context.Background(), "schedule_run", map[string]any{"task_id": "task_broken"})
	if err != nil {
		t.Fatalf("schedule_run failed: %v", err)
	}
	if !strings.Contains(res, "FAILED") || !strings.Contains(res, "no default model configured") {
		t.Errorf("failure must be reported with its reason:\n%s", res)
	}
	// The failure must be actionable: a failed task is the agent's job to fix,
	// not only to relay.
	for _, want := range []string{"session_read", "schedule_update", "schedule_run again"} {
		if !strings.Contains(res, want) {
			t.Errorf("failure guidance must mention %q:\n%s", want, res)
		}
	}
}

func TestScheduleRunWithoutWaitReturnsImmediately(t *testing.T) {
	mock := &mockSchedulerService{
		tasks: []ScheduledTaskInfo{{
			ID:             "task_slow",
			Type:           "agent_task",
			Title:          "Slow task",
			Prompt:         "Take your time",
			NotifyChannels: []string{"web"},
			Status:         "pending",
		}},
		manual: &mockManualRun{hold: true},
	}
	r := newSchedulerRunRegistry(t, mock)

	start := time.Now()
	res, err := r.execute(context.Background(), "schedule_run", map[string]any{"task_id": "task_slow", "wait_seconds": 1})
	if err != nil {
		t.Fatalf("schedule_run failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("returned after %v without waiting for the window", elapsed)
	}
	if !strings.Contains(res, "keeps running") {
		t.Errorf("slow run must report that it keeps running:\n%s", res)
	}

	if _, err := r.execute(context.Background(), "schedule_run", map[string]any{"task_id": "unknown"}); err == nil {
		t.Error("expected an unknown task to fail")
	}
}

func TestScheduleRunExecTimeoutDefaultsToManualWaitWindow(t *testing.T) {
	if got := scheduleRunExecTimeout(map[string]any{}); got != defaultManualRunWaitSeconds+15 {
		t.Fatalf("default timeout = %d, want %d", got, defaultManualRunWaitSeconds+15)
	}
	if got := scheduleRunExecTimeout(map[string]any{"wait_seconds": 0}); got != 15 {
		t.Fatalf("zero-wait timeout = %d, want 15", got)
	}
	if got := scheduleRunExecTimeout(map[string]any{"wait_seconds": 999}); got != maxManualRunWaitSeconds+15 {
		t.Fatalf("clamped timeout = %d, want %d", got, maxManualRunWaitSeconds+15)
	}
}
