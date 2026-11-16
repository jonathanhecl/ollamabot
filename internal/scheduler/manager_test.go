package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
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
	if task.NotifyMode != NotifyModeAlways {
		t.Fatalf("legacy notify mode = %q, want always", task.NotifyMode)
	}
}

func TestManager_NotifyModes(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	reminder, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:    "web",
		Type:       "alert",
		Prompt:     "Take a break",
		When:       "0 9 * * *",
		NotifyMode: "on_alert",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reminder.NotifyMode != "always" {
		t.Fatalf("reminders must always notify, got %q", reminder.NotifyMode)
	}

	task, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:    "web",
		Type:       "agent_task",
		Prompt:     "Check the news",
		When:       "every 5h",
		NotifyMode: "on_alert",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.NotifyMode != "on_alert" {
		t.Fatalf("notify mode = %q, want on_alert", task.NotifyMode)
	}

	updated, err := mgr.UpdateScheduledTask(task.ID, tools.ScheduledTaskPatch{NotifyMode: "always"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.NotifyMode != "always" {
		t.Fatalf("updated notify mode = %q, want always", updated.NotifyMode)
	}
}

func TestManager_SilentRunOnlyNotifiesOnAlert(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})
	ch := make(chan string, 2)
	mgr.RegisterNotifier("web", func(task Task, message string) error {
		ch <- message
		return nil
	})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:        "web",
		Type:           "agent_task",
		Title:          "News watch",
		Prompt:         "Check for news about X",
		When:           "every 5h",
		NotifyChannels: []string{"web"},
		NotifyMode:     "on_alert",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing new: the run is recorded in the session but no message is sent.
	mgr.finishAgentRun(task, time.Now(), nil, "Nothing new happened", nil, false)
	select {
	case msg := <-ch:
		t.Fatalf("unexpected notification for a silent run: %s", msg)
	case <-time.After(150 * time.Millisecond):
	}
	updated, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastObservation != "Nothing new happened" {
		t.Fatalf("last observation = %q", updated.LastObservation)
	}

	// The agent reported news: the user gets the alert text.
	mgr.finishAgentRun(updated, time.Now(), nil, "There is news", []string{"Something important happened"}, false)
	select {
	case msg := <-ch:
		if !strings.Contains(msg, "Something important happened") || !strings.Contains(msg, "News watch") {
			t.Fatalf("unexpected alert message %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the alert")
	}

	updated, err = mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.LastAlert, "Something important happened") || updated.LastAlertAt == nil {
		t.Fatalf("alert state not persisted: %+v", updated)
	}
}

func waitOutcome(t *testing.T, ch <-chan tools.ManualRunOutcome) tools.ManualRunOutcome {
	t.Helper()
	select {
	case outcome, ok := <-ch:
		if !ok {
			t.Fatal("forced run closed without an outcome")
		}
		return outcome
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the forced run")
	}
	return tools.ManualRunOutcome{}
}

func TestManager_ForcedRunOfReminderKeepsSchedule(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})
	notified := make(chan string, 1)
	mgr.RegisterNotifier("web", func(task Task, message string) error {
		notified <- message
		return nil
	})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Type:    "alert",
		Title:   "Water",
		Prompt:  "Drink water",
		When:    "0 9 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}

	outCh, started, err := mgr.RunScheduledTaskNow(info.ID)
	if err != nil {
		t.Fatalf("forced run rejected: %v", err)
	}
	if started.ID != info.ID {
		t.Fatalf("started task = %q, want %q", started.ID, info.ID)
	}
	outcome := waitOutcome(t, outCh)
	if outcome.Err != "" {
		t.Fatalf("forced run failed: %s", outcome.Err)
	}

	// The requester asked for the run from a chat: the reminder comes back in
	// the outcome and the task's channels are not touched.
	if !strings.Contains(outcome.Answer, "Drink water") {
		t.Fatalf("outcome = %q, want the reminder text", outcome.Answer)
	}
	select {
	case msg := <-notified:
		t.Fatalf("a forced run must not notify the task channels, got %q", msg)
	case <-time.After(200 * time.Millisecond):
	}

	after, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.NextRunAt.Equal(before.NextRunAt) {
		t.Fatalf("forced run moved the schedule: %v -> %v", before.NextRunAt, after.NextRunAt)
	}
	if after.Status != TaskStatusPending {
		t.Fatalf("status = %q, want pending", after.Status)
	}
	if after.RunCount != 1 || after.LastRunAt == nil {
		t.Fatalf("run not recorded: count=%d last=%v", after.RunCount, after.LastRunAt)
	}
}

func TestManager_ForcedRunOfPausedTaskRunsWithoutResuming(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})
	notified := make(chan string, 1)
	mgr.RegisterNotifier("web", func(task Task, message string) error {
		notified <- message
		return nil
	})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Type:    "alert",
		Prompt:  "Stretch",
		When:    "0 9 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.UpdateScheduledTask(info.ID, tools.ScheduledTaskPatch{Status: "paused"}); err != nil {
		t.Fatal(err)
	}

	outCh, _, err := mgr.RunScheduledTaskNow(info.ID)
	if err != nil {
		t.Fatalf("forced run of a paused task rejected: %v", err)
	}
	waitOutcome(t, outCh)

	select {
	case msg := <-notified:
		t.Fatalf("a forced run of a paused task must not notify, got %q", msg)
	case <-time.After(200 * time.Millisecond):
	}

	after, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != TaskStatusPaused {
		t.Fatalf("status = %q, want still paused", after.Status)
	}
}

func TestManager_ForcedRunOfAgentTaskKeepsScheduleOnFailure(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})
	notified := make(chan string, 1)
	mgr.RegisterNotifier("web", func(task Task, message string) error {
		notified <- message
		return nil
	})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Type:    "agent_task",
		Prompt:  "Check the news",
		When:    "every 5h",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}

	// No model is configured in this test: the run has to report the failure
	// without touching the schedule or completing the task.
	outCh, _, err := mgr.RunScheduledTaskNow(info.ID)
	if err != nil {
		t.Fatalf("forced run rejected: %v", err)
	}
	outcome := waitOutcome(t, outCh)
	if outcome.Err == "" {
		t.Fatal("expected the forced run to report the failure")
	}

	select {
	case msg := <-notified:
		t.Fatalf("a run that never reached the model must not notify: %q", msg)
	case <-time.After(200 * time.Millisecond):
	}

	after, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.NextRunAt.Equal(before.NextRunAt) {
		t.Fatalf("failed forced run moved the schedule: %v -> %v", before.NextRunAt, after.NextRunAt)
	}
	if after.Status != TaskStatusPending {
		t.Fatalf("status = %q, want pending", after.Status)
	}
	if after.LastError == "" {
		t.Fatal("expected the failure to be recorded")
	}
}

// The prompt has to state what the run cannot do: a scheduled run writes the
// report, it does not deliver it and it does not manage other tasks. A model
// that plans to "send it to web and telegram" stalls or fails.
func TestScheduledTaskPromptStatesDeliveryIsAutomatic(t *testing.T) {
	prompt := scheduledTaskSystemPrompt(Task{Type: TaskTypeAgentTask, Prompt: "Summarise the news"})

	for _, want := range []string{"final answer IS the delivery", "Never try to send it yourself", "do not create, modify or run scheduled tasks"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("scheduled task prompt must contain %q:\n%s", want, prompt)
		}
	}
}

// A scheduled run must not keep the tool output of every previous run in its
// context: the history grows without bound and the run ends up answering
// nothing at all.
func TestManager_ScheduledRunStartsWithAFreshContext(t *testing.T) {
	sessionsPath := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(sessionsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	store := sessions.NewStore(sessionsPath)

	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(raw))
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done:    true,
				Message: ollama.Message{Role: "assistant", Content: "Digest ready."},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	mgr := newTestManager(t, config.Config{
		ServerEnabled:      true,
		SessionsPath:       sessionsPath,
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
	})
	mgr.sessionStore = store
	mgr.client = ollama.NewClient(server.URL)

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Type:    "agent_task",
		Title:   "Digest",
		Prompt:  "Summarise the news",
		When:    "0 */6 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}

	runOnce := func() {
		outCh, _, err := mgr.RunScheduledTaskNow(info.ID)
		if err != nil {
			t.Fatalf("forced run rejected: %v", err)
		}
		waitOutcome(t, outCh)
	}
	runOnce()

	task, err := mgr.GetTask(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(task.SessionID) == "" {
		t.Fatal("the run must log into a session")
	}

	// Simulate what a previous run leaves behind: a wall of tool output.
	sess, err := store.Get(task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := json.Marshal(sessions.RawMsg{
		Role:    "assistant",
		Content: "OLD-RUN-TOOL-OUTPUT " + strings.Repeat("arxiv entry ", 200),
	})
	sess.Messages = append(sess.Messages, stale)
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	bodies = nil
	mu.Unlock()

	runOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("the second run never called the model")
	}
	for _, body := range bodies {
		if strings.Contains(body, "OLD-RUN-TOOL-OUTPUT") {
			t.Fatalf("the run reloaded the history of a previous run into its context:\n%s", truncateForTest(body))
		}
		if !strings.Contains(body, "Summarise the news") {
			t.Fatalf("the run lost its own instruction:\n%s", truncateForTest(body))
		}
	}
}

func truncateForTest(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

func TestManager_ForcedRunRejectsWhileRunning(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Type:    "alert",
		Prompt:  "Busy",
		When:    "0 9 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}

	mgr.mu.Lock()
	mgr.isExecuting[info.ID] = true
	mgr.mu.Unlock()

	if _, _, err := mgr.RunScheduledTaskNow(info.ID); err == nil {
		t.Fatal("expected the second forced run to be rejected")
	}

	mgr.mu.Lock()
	delete(mgr.isExecuting, info.ID)
	mgr.mu.Unlock()

	if _, _, err := mgr.RunScheduledTaskNow("rem_missing"); err == nil {
		t.Fatal("expected an unknown task to be rejected")
	}
}

// The plan monitor uses this lookup to leave task sessions alone: a plan there
// was approved by the run itself and nobody is waiting on it.
func TestManager_IsTaskSession(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	info, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web",
		Type:    "agent_task",
		Prompt:  "News digest",
		When:    "0 */6 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}

	if mgr.IsTaskSession("") {
		t.Fatal("an empty session ID is never a task session")
	}
	if mgr.IsTaskSession("sess_unknown") {
		t.Fatal("expected an unknown session to be ignored")
	}

	mgr.mu.Lock()
	task := mgr.tasks[info.ID]
	task.SessionID = "sess_task_run"
	mgr.tasks[info.ID] = task
	mgr.mu.Unlock()

	if !mgr.IsTaskSession("sess_task_run") {
		t.Fatal("expected the run session of a task to be recognised")
	}
	if mgr.IsTaskSession("sess_other") {
		t.Fatal("expected another session to be ignored")
	}
}
