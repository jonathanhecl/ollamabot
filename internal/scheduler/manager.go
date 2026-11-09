package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/mcp"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// NotificationFunc is called when a task triggers and needs to deliver a message to a user/channel.
type NotificationFunc func(task Task, message string) error

// Manager coordinates scheduled reminders and recurring background tasks.
type Manager struct {
	mu           sync.RWMutex
	cfgMgr       *config.Manager
	client       *ollama.Client
	memoryStore  *memory.Store
	mcpMgr       *mcp.Manager
	sessionStore *sessions.Store
	storagePath  string
	tasks        map[string]Task
	notifiers    map[string]NotificationFunc
	cancelFunc   context.CancelFunc
	tickerDone   chan struct{}
	interval     time.Duration
	isExecuting  map[string]bool
}

const scheduledTaskSystemPromptBase = `You are OllamaBot executing a scheduled task on behalf of the user, who is not present right now.
Use your available tools (web search, MCP tools, memory, files, etc.) to accomplish the task thoroughly.
When finished, write a self-contained report the user can read without any other context: the answer first, then the key details.
Do not ask questions and do not wait for confirmation; nobody will reply. If something is impossible, say exactly what failed.`

// scheduledTaskSystemPrompt adds the silent-monitoring contract and the
// previous observation so the model can decide whether something is new.
func scheduledTaskSystemPrompt(task Task) string {
	prompt := scheduledTaskSystemPromptBase
	if task.NotifyMode != NotifyModeOnAlert {
		return prompt
	}

	prompt += `

This is a SILENT MONITORING task. The user only wants a message when the condition described in the task is met, or when something materially new appeared since the previous observation.
Call send_scheduled_alert with the exact user-facing message when that happens. If nothing new happened, or the condition is not met, finish normally WITHOUT calling it: the user will not be notified.
Do not repeat an alert that has no material change.`

	if obs := strings.TrimSpace(task.LastObservation); obs != "" {
		prompt += "\n\nPrevious run observation:\n" + truncateTaskText(obs, 2000)
	}
	if alert := strings.TrimSpace(task.LastAlert); alert != "" {
		when := ""
		if task.LastAlertAt != nil {
			when = " (" + task.LastAlertAt.Format("2006-01-02 15:04") + ")"
		}
		prompt += "\n\nLast alert sent" + when + ":\n" + truncateTaskText(alert, 2000)
	}
	return prompt
}

func truncateTaskText(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func normalizeNotifyMode(mode string, taskType TaskType) string {
	if taskType == TaskTypeAgentTask && strings.TrimSpace(strings.ToLower(mode)) == NotifyModeOnAlert {
		return NotifyModeOnAlert
	}
	return NotifyModeAlways
}

func (m *Manager) config() config.Config {
	return m.cfgMgr.Get()
}

// NewManager creates a new scheduler Manager.
func NewManager(cfgMgr *config.Manager, client *ollama.Client, memoryStore *memory.Store, mcpMgr *mcp.Manager) *Manager {
	sessionsPath := cfgMgr.Get().SessionsPath
	if memoryStore == nil {
		memoryStore = memory.NewStore(cfgMgr.Get().MemoryPath)
	}
	sStore := sessions.NewStore(sessionsPath)
	storagePath := filepath.Join(sessionsPath, "scheduler_tasks.json")

	m := &Manager{
		cfgMgr:       cfgMgr,
		client:       client,
		memoryStore:  memoryStore,
		mcpMgr:       mcpMgr,
		sessionStore: sStore,
		storagePath:  storagePath,
		tasks:        make(map[string]Task),
		notifiers:    make(map[string]NotificationFunc),
		interval:     10 * time.Second,
		isExecuting:  make(map[string]bool),
	}
	_ = m.Load()
	return m
}

// RegisterNotifier registers a delivery callback for a channel (e.g. "telegram", "web").
func (m *Manager) RegisterNotifier(channel string, fn NotificationFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifiers[channel] = fn
}

// UnregisterNotifier removes a delivery callback.
func (m *Manager) UnregisterNotifier(channel string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.notifiers, channel)
}

// notify delivers the message to every channel the task targets. Delivery is
// fan-out: a task created in the web chat can also push to Telegram and vice
// versa.
func (m *Manager) notify(task Task, message string) {
	channels := task.NotifyChannels
	if len(channels) == 0 {
		channels = []string{task.Channel}
	}

	m.mu.RLock()
	targets := make(map[string]NotificationFunc, len(channels))
	for _, ch := range channels {
		if fn, ok := m.notifiers[ch]; ok && fn != nil {
			targets[ch] = fn
		}
	}
	m.mu.RUnlock()

	if len(targets) == 0 {
		log.Printf("[scheduler] No notifier registered for channels %v (task %s)", channels, task.ID)
		return
	}

	for channel, fn := range targets {
		channel, fn := channel, fn
		go func() {
			if err := fn(task, message); err != nil {
				log.Printf("[scheduler] Notification failed for task %s on channel %s: %v", task.ID, channel, err)
			}
		}()
	}
}

// Start launches the background ticker.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.cancelFunc != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel
	m.tickerDone = make(chan struct{})
	interval := m.interval
	m.mu.Unlock()

	ticker := time.NewTicker(interval)
	go func() {
		defer close(m.tickerDone)
		log.Println("[scheduler] Background scheduler started")
		for {
			select {
			case <-ticker.C:
				m.Tick(ctx)
			case <-ctx.Done():
				ticker.Stop()
				log.Println("[scheduler] Background scheduler stopped")
				return
			}
		}
	}()
}

// Stop terminates the scheduler loop.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancelFunc == nil {
		m.mu.Unlock()
		return
	}
	m.cancelFunc()
	m.cancelFunc = nil
	m.mu.Unlock()

	<-m.tickerDone
}

// Load reads tasks from disk and migrates legacy records in place.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var list []Task
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	m.tasks = make(map[string]Task, len(list))
	for _, t := range list {
		m.migrateTask(&t)
		m.tasks[t.ID] = t
	}
	return nil
}

// migrateTask fills fields introduced after the first scheduler release.
func (m *Manager) migrateTask(t *Task) {
	if strings.TrimSpace(t.Title) == "" {
		t.Title = deriveTaskTitle("", t.Prompt)
	}
	if t.OriginSessionID == "" {
		t.OriginSessionID = t.SessionID
		// Legacy records stored the origin chat as SessionID. Runs now get a
		// dedicated session, so the origin is preserved separately.
		t.SessionID = ""
	}
	if len(t.NotifyChannels) == 0 {
		t.NotifyChannels = m.defaultNotifyChannels(t.Channel)
	}
	if strings.TrimSpace(t.NotifyMode) == "" {
		t.NotifyMode = NotifyModeAlways
	}
	t.NotifyMode = normalizeNotifyMode(t.NotifyMode, t.Type)
}

// Save writes tasks to disk atomically.
func (m *Manager) Save() error {
	m.mu.RLock()
	list := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		list = append(list, t)
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(m.storagePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".scheduler-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, m.storagePath)
}

// AddTask adds and validates a task.
func (m *Manager) AddTask(task Task) (Task, error) {
	if strings.TrimSpace(task.Prompt) == "" {
		return Task{}, fmt.Errorf("task prompt/content cannot be empty")
	}
	if task.ID == "" {
		task.ID = GenerateTaskID()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.Status == "" {
		task.Status = TaskStatusPending
	}
	if task.Channel == "" {
		task.Channel = "web"
	}
	task.Title = deriveTaskTitle(task.Title, task.Prompt)
	if len(task.NotifyChannels) == 0 {
		task.NotifyChannels = m.defaultNotifyChannels(task.Channel)
	} else {
		task.NotifyChannels = normalizeChannels(task.NotifyChannels, task.Channel)
	}
	task.NotifyMode = normalizeNotifyMode(task.NotifyMode, task.Type)
	m.ensureTaskSession(&task)

	m.mu.Lock()
	m.tasks[task.ID] = task
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		log.Printf("[scheduler] Failed to persist task %s: %v", task.ID, err)
	}
	return task, nil
}

func (t Task) ToInfo() tools.ScheduledTaskInfo {
	return tools.ScheduledTaskInfo{
		ID:              t.ID,
		Type:            string(t.Type),
		ScheduleType:    string(t.ScheduleType),
		Title:           t.Title,
		Prompt:          t.Prompt,
		CronExpr:        t.CronExpr,
		IntervalStr:     t.IntervalStr,
		Channel:         t.Channel,
		OriginSessionID: t.OriginSessionID,
		SessionID:       t.SessionID,
		NotifyChannels:  append([]string(nil), t.NotifyChannels...),
		NotifyMode:      t.NotifyMode,
		LastObservation: t.LastObservation,
		LastAlert:       t.LastAlert,
		LastAlertAt:     t.LastAlertAt,
		NextRunAt:       t.NextRunAt,
		LastRunAt:       t.LastRunAt,
		Status:          string(t.Status),
		LastError:       t.LastError,
		RunCount:        t.RunCount,
	}
}

// CreateScheduledTask creates a reminder (alert) or an autonomous agent task.
func (m *Manager) CreateScheduledTask(spec tools.ScheduledTaskSpec) (tools.ScheduledTaskInfo, error) {
	now := time.Now()
	schedType, nextRun, expr, err := ParseSchedule(spec.When, now)
	if err != nil {
		return tools.ScheduledTaskInfo{}, fmt.Errorf("invalid schedule format %q: %w", spec.When, err)
	}

	tType := TaskTypeAlert
	switch spec.Type {
	case "agent_task":
		tType = TaskTypeAgentTask
	case "", "alert":
		tType = TaskTypeAlert
	default:
		return tools.ScheduledTaskInfo{}, fmt.Errorf("unknown task type %q", spec.Type)
	}

	origin := strings.TrimSpace(spec.OriginSessionID)
	task := Task{
		ID:              GenerateTaskID(),
		Type:            tType,
		ScheduleType:    schedType,
		Title:           deriveTaskTitle(spec.Title, spec.Prompt),
		Prompt:          spec.Prompt,
		Channel:         spec.Channel,
		OriginSessionID: origin,
		TargetChatID:    spec.TargetChatID,
		NotifyChannels:  spec.NotifyChannels,
		NotifyMode:      normalizeNotifyMode(spec.NotifyMode, tType),
		CreatedAt:       now,
		NextRunAt:       nextRun,
		Status:          TaskStatusPending,
	}

	if schedType == ScheduleTypeCron {
		task.CronExpr = expr
	} else if schedType == ScheduleTypeInterval {
		task.IntervalStr = expr
	}

	added, err := m.AddTask(task)
	if err != nil {
		return tools.ScheduledTaskInfo{}, err
	}
	return added.ToInfo(), nil
}

// UpdateScheduledTask edits an existing task in place.
func (m *Manager) UpdateScheduledTask(id string, patch tools.ScheduledTaskPatch) (tools.ScheduledTaskInfo, error) {
	now := time.Now()

	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return tools.ScheduledTaskInfo{}, fmt.Errorf("task %q not found", id)
	}

	if strings.TrimSpace(patch.Title) != "" {
		task.Title = deriveTaskTitle(patch.Title, task.Prompt)
	}
	if strings.TrimSpace(patch.Prompt) != "" {
		task.Prompt = patch.Prompt
	}
	if strings.TrimSpace(patch.When) != "" {
		schedType, nextRun, expr, err := ParseSchedule(patch.When, now)
		if err != nil {
			m.mu.Unlock()
			return tools.ScheduledTaskInfo{}, fmt.Errorf("invalid schedule format %q: %w", patch.When, err)
		}
		task.ScheduleType = schedType
		task.NextRunAt = nextRun
		task.CronExpr = ""
		task.IntervalStr = ""
		if schedType == ScheduleTypeCron {
			task.CronExpr = expr
		} else if schedType == ScheduleTypeInterval {
			task.IntervalStr = expr
		}
	}
	if patch.NotifyChannels != nil {
		task.NotifyChannels = normalizeChannels(patch.NotifyChannels, task.Channel)
	}
	if strings.TrimSpace(patch.NotifyMode) != "" {
		task.NotifyMode = normalizeNotifyMode(patch.NotifyMode, task.Type)
	}

	switch strings.ToLower(strings.TrimSpace(patch.Status)) {
	case "paused":
		task.Status = TaskStatusPaused
	case "pending", "resumed", "active":
		task.Status = TaskStatusPending
		if !task.NextRunAt.After(now) {
			m.reschedule(&task, now)
		}
	}

	m.ensureTaskSession(&task)
	m.syncTaskSessionTitle(&task)

	m.tasks[id] = task
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		return tools.ScheduledTaskInfo{}, err
	}
	return task.ToInfo(), nil
}

// reschedule moves a task that is already due to its next sensible run.
func (m *Manager) reschedule(task *Task, now time.Time) {
	switch task.ScheduleType {
	case ScheduleTypeCron:
		if next, err := NextCronTime(task.CronExpr, now); err == nil {
			task.NextRunAt = next
			return
		}
	case ScheduleTypeInterval:
		if dur, ok := parseCustomDuration(task.IntervalStr); ok && dur > 0 {
			task.NextRunAt = now.Add(dur)
			return
		}
	}
	task.NextRunAt = now
}

// ensureTaskSession creates the dedicated session that shows a task's output in
// the web UI. Only web-delivered tasks need one; agent runs create theirs
// lazily if web is not a delivery channel.
func (m *Manager) ensureTaskSession(task *Task) {
	if task == nil || task.SessionID != "" || m.sessionStore == nil {
		return
	}
	if !containsChannel(task.NotifyChannels, "web") {
		return
	}
	cfg := m.config()
	model := config.ResolveModel(cfg, config.ModelRoleMain)
	if strings.TrimSpace(model) == "" {
		model = cfg.OllamaDefaultModel
	}
	sess := sessions.Session{
		ID:    sessions.GenerateID(),
		Title: taskSessionTitle(*task),
		Model: model,
	}
	if err := m.sessionStore.Save(sess); err != nil {
		log.Printf("[scheduler] Failed to create session for task %s: %v", task.ID, err)
		return
	}
	task.SessionID = sess.ID
	sessions.NotifyUpdate(sess.ID)
}

// syncTaskSessionTitle keeps the task's session label aligned with its title.
func (m *Manager) syncTaskSessionTitle(task *Task) {
	if task == nil || task.SessionID == "" || m.sessionStore == nil {
		return
	}
	sess, err := m.sessionStore.Get(task.SessionID)
	if err != nil {
		return
	}
	title := taskSessionTitle(*task)
	if sess.Title == title {
		return
	}
	// Only rename sessions owned by the scheduler; never touch a real chat.
	if !strings.HasPrefix(sess.Title, "⏰ ") {
		return
	}
	sess.Title = title
	if err := m.sessionStore.Save(sess); err != nil {
		log.Printf("[scheduler] Failed to rename session for task %s: %v", task.ID, err)
		return
	}
	sessions.NotifyUpdate(sess.ID)
}

func containsChannel(channels []string, want string) bool {
	for _, ch := range channels {
		if ch == want {
			return true
		}
	}
	return false
}

// DeleteScheduledTask removes a task permanently.
func (m *Manager) DeleteScheduledTask(id string) error {
	m.mu.Lock()
	if _, ok := m.tasks[id]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("task %q not found", id)
	}
	delete(m.tasks, id)
	m.mu.Unlock()
	return m.Save()
}

// CreateReminder is a helper to schedule a simple text alert.
func (m *Manager) CreateReminder(channel string, sessionID string, targetChatID int64, text string, when string) (tools.ScheduledTaskInfo, error) {
	return m.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:         channel,
		OriginSessionID: sessionID,
		TargetChatID:    targetChatID,
		Type:            "alert",
		Prompt:          text,
		When:            when,
	})
}

// CreateTask is a helper to schedule an autonomous agent execution.
func (m *Manager) CreateTask(channel string, sessionID string, targetChatID int64, instruction string, when string, isAgentTask bool) (tools.ScheduledTaskInfo, error) {
	tType := "alert"
	if isAgentTask {
		tType = "agent_task"
	}
	return m.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel:         channel,
		OriginSessionID: sessionID,
		TargetChatID:    targetChatID,
		Type:            tType,
		Prompt:          instruction,
		When:            when,
	})
}

// CancelTask marks a task as cancelled.
func (m *Manager) CancelTask(id string) error {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("task %q not found", id)
	}
	task.Status = TaskStatusCancelled
	m.tasks[id] = task
	m.mu.Unlock()

	return m.Save()
}

// GetTask returns a task by ID.
func (m *Manager) GetTask(id string) (Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task %q not found", id)
	}
	return t, nil
}

// ListTasks returns active or all tasks filtered optionally by session or channel.
func (m *Manager) ListTasks(channel string, sessionID string, includeCompleted bool) []tools.ScheduledTaskInfo {
	raw := m.ListRawTasks(channel, sessionID, includeCompleted)
	result := make([]tools.ScheduledTaskInfo, 0, len(raw))
	for _, t := range raw {
		result = append(result, t.ToInfo())
	}
	return result
}

// ListRawTasks returns tasks as raw Task structs.
func (m *Manager) ListRawTasks(channel string, sessionID string, includeCompleted bool) []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Task
	for _, t := range m.tasks {
		if !includeCompleted && (t.Status == TaskStatusCompleted || t.Status == TaskStatusCancelled) {
			continue
		}
		if channel != "" && t.Channel != channel {
			continue
		}
		if sessionID != "" && t.SessionID != sessionID {
			continue
		}
		result = append(result, t)
	}
	return result
}

// Tick evaluates pending tasks whose NextRunAt has arrived.
func (m *Manager) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	now := time.Now()
	var dueTasks []Task

	m.mu.RLock()
	for _, t := range m.tasks {
		if t.Status == TaskStatusPending && (now.After(t.NextRunAt) || now.Equal(t.NextRunAt)) {
			if !m.isExecuting[t.ID] {
				dueTasks = append(dueTasks, t)
			}
		}
	}
	m.mu.RUnlock()

	for _, t := range dueTasks {
		if ctx.Err() != nil {
			return
		}
		m.executeTask(ctx, t)
	}
}

func (m *Manager) executeTask(ctx context.Context, task Task) {
	m.mu.Lock()
	m.isExecuting[task.ID] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.isExecuting, task.ID)
		m.mu.Unlock()
	}()

	now := time.Now()

	if task.Type == TaskTypeAlert {
		msg := fmt.Sprintf("⏰ **Reminder**: %s", task.Prompt)
		m.updateAfterRun(task, now, nil)
		m.notify(task, msg)
		return
	}

	m.executeAgentTask(ctx, task, now)
}

func (m *Manager) executeAgentTask(ctx context.Context, task Task, now time.Time) {
	m.mu.RLock()
	_, exists := m.tasks[task.ID]
	m.mu.RUnlock()
	if !exists {
		return
	}

	cfg := m.config()
	model := config.ResolveModel(cfg, config.ModelRoleMain)
	if strings.TrimSpace(model) == "" {
		model = cfg.OllamaDefaultModel
	}
	if strings.TrimSpace(model) == "" {
		m.updateAfterRun(task, now, fmt.Errorf("no default model configured"))
		return
	}

	if m.sessionStore != nil && strings.TrimSpace(task.SessionID) == "" {
		sess := sessions.Session{
			ID:    sessions.GenerateID(),
			Title: taskSessionTitle(task),
			Model: model,
		}
		if err := m.sessionStore.Save(sess); err != nil {
			m.updateAfterRun(task, now, fmt.Errorf("failed to create run session: %w", err))
			return
		}
		task.SessionID = sess.ID
		m.mu.Lock()
		m.tasks[task.ID] = task
		m.mu.Unlock()
		if err := m.Save(); err != nil {
			log.Printf("[scheduler] Failed to persist run session for task %s: %v", task.ID, err)
		}
		sessions.NotifyUpdate(task.SessionID)
	}

	log.Printf("[scheduler] Executing autonomous task %s in session %s: %q", task.ID, task.SessionID, task.Prompt)

	silent := task.NotifyMode == NotifyModeOnAlert
	var alerts []string

	deps := engine.Deps{
		ConfigMgr:        m.cfgMgr,
		Client:           m.client,
		SessionStore:     m.sessionStore,
		MemoryStore:      m.memoryStore,
		MCPManager:       m.mcpMgr,
		SchedulerManager: m,
		// Nobody is present to approve risky tools during a scheduled run.
		ApprovalHandler: denyApprovals{},
	}
	if silent {
		deps.ScheduledAlertHandler = func(message string) {
			if text := strings.TrimSpace(message); text != "" {
				alerts = append(alerts, text)
			}
		}
	}

	messages := []router.MediaMessage{
		{Message: ollama.Message{Role: "system", Content: scheduledTaskSystemPrompt(task)}},
		{Message: ollama.Message{Role: "user", Content: task.Prompt}},
	}

	result, err := engine.ProcessTurn(ctx, deps, engine.TurnRequest{
		SessionID:  task.SessionID,
		Channel:    "scheduler",
		Background: true,
		Messages:   messages,
	})
	if errors.Is(err, engine.ErrBackgroundBusy) {
		// Another background loop (sleep, goal) is using the model. Leave the
		// task pending so the next tick retries it.
		log.Printf("[scheduler] Background slot busy, deferring agent task %s", task.ID)
		return
	}

	answer := ""
	if err == nil {
		answer = strings.TrimSpace(result.FinalAnswer)
	}
	m.finishAgentRun(task, now, err, answer, alerts)
}

// finishAgentRun persists the run outcome and decides whether the user gets a
// message. Silent monitoring tasks only notify when the agent called
// send_scheduled_alert; failures always notify.
func (m *Manager) finishAgentRun(task Task, now time.Time, runErr error, answer string, alerts []string) {
	m.mu.Lock()
	current, ok := m.tasks[task.ID]
	if !ok {
		m.mu.Unlock()
		log.Printf("[scheduler] Task %s was deleted while running; keeping it deleted", task.ID)
		return
	}
	current.LastRunAt = &now
	current.RunCount++
	if runErr != nil {
		current.LastError = runErr.Error()
		if current.Status != TaskStatusPaused {
			if current.ScheduleType == ScheduleTypeOnce {
				current.Status = TaskStatusFailed
			} else {
				m.advanceOrComplete(&current, now)
			}
		}
	} else {
		current.LastError = ""
		if strings.TrimSpace(answer) != "" {
			current.LastObservation = truncateTaskText(answer, 4000)
		}
		if len(alerts) > 0 {
			current.LastAlert = truncateTaskText(strings.Join(alerts, "\n\n"), 4000)
			current.LastAlertAt = &now
		}
		if current.Status != TaskStatusPaused {
			m.advanceOrComplete(&current, now)
		}
	}
	m.tasks[task.ID] = current
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		log.Printf("[scheduler] Failed to persist task %s after run: %v", task.ID, err)
	}

	if runErr != nil {
		log.Printf("[scheduler] Agent task %s failed: %v", task.ID, runErr)
		m.notify(task, fmt.Sprintf("⚠️ **Scheduled Task Failed** (%s):\nError: %v", task.Prompt, runErr))
		return
	}

	if current.NotifyMode == NotifyModeOnAlert {
		if len(alerts) == 0 {
			return
		}
		title := deriveTaskTitle(current.Title, current.Prompt)
		m.notify(task, fmt.Sprintf("🔔 **%s**\n\n%s", title, strings.Join(alerts, "\n\n")))
		return
	}

	response := answer
	if response == "" {
		response = "Task completed successfully with no additional output."
	}
	m.notify(task, fmt.Sprintf("📋 **Scheduled Task Report**\n**Task**: %s\n\n%s", task.Prompt, response))
}

// updateAfterRun records the run outcome and advances the schedule. It re-reads
// the stored task so edits or deletions made while the run was executing are
// not overwritten by the stale copy that started the run.
func (m *Manager) updateAfterRun(task Task, now time.Time, runErr error) {
	m.mu.Lock()
	current, ok := m.tasks[task.ID]
	if !ok {
		m.mu.Unlock()
		log.Printf("[scheduler] Task %s was deleted while running; keeping it deleted", task.ID)
		return
	}
	current.LastRunAt = &now
	current.RunCount++
	if runErr != nil {
		current.LastError = runErr.Error()
		if current.Status != TaskStatusPaused {
			if current.ScheduleType == ScheduleTypeOnce {
				current.Status = TaskStatusFailed
			} else {
				m.advanceOrComplete(&current, now)
			}
		}
	} else {
		current.LastError = ""
		if current.Status != TaskStatusPaused {
			m.advanceOrComplete(&current, now)
		}
	}
	m.tasks[task.ID] = current
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		log.Printf("[scheduler] Failed to persist task %s after run: %v", task.ID, err)
	}
}

func (m *Manager) advanceOrComplete(task *Task, now time.Time) {
	switch task.ScheduleType {
	case ScheduleTypeOnce:
		task.Status = TaskStatusCompleted
	case ScheduleTypeInterval:
		if dur, ok := parseCustomDuration(task.IntervalStr); ok && dur > 0 {
			task.NextRunAt = now.Add(dur)
			task.Status = TaskStatusPending
		} else {
			task.Status = TaskStatusCompleted
		}
	case ScheduleTypeCron:
		if next, err := NextCronTime(task.CronExpr, now); err == nil {
			task.NextRunAt = next
			task.Status = TaskStatusPending
		} else {
			task.LastError = fmt.Sprintf("failed to compute next cron time: %v", err)
			task.Status = TaskStatusFailed
		}
	default:
		task.Status = TaskStatusCompleted
	}
}

// denyApprovals keeps the risk gate active during unattended runs: risky tools
// fail with a message instead of waiting for a button nobody can press.
type denyApprovals struct{}

func (denyApprovals) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	return false, nil
}

// defaultNotifyChannels picks the channels a new task reports to. A task
// created from any channel reaches web and Telegram when they are available.
func (m *Manager) defaultNotifyChannels(origin string) []string {
	cfg := m.config()
	return buildNotifyChannels(nil, origin, cfg.ServerEnabled, cfg.TelegramBotToken != "")
}

// normalizeChannels sanitizes an explicit channel list. An empty list falls
// back to the origin channel so a task never ends up undeliverable.
func normalizeChannels(channels []string, origin string) []string {
	return buildNotifyChannels(channels, origin, false, false)
}

func buildNotifyChannels(channels []string, origin string, serverEnabled, telegramEnabled bool) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 3)
	add := func(ch string) {
		ch = strings.ToLower(strings.TrimSpace(ch))
		if ch == "" || seen[ch] {
			return
		}
		seen[ch] = true
		out = append(out, ch)
	}

	explicit := false
	for _, ch := range channels {
		if ch != "web" && ch != "telegram" {
			continue
		}
		explicit = true
		add(ch)
	}

	if !explicit {
		add(origin)
		if serverEnabled {
			add("web")
		}
		if telegramEnabled {
			add("telegram")
		}
	}
	if len(out) == 0 {
		if strings.TrimSpace(origin) != "" {
			add(origin)
		} else {
			add("web")
		}
	}
	return out
}

func deriveTaskTitle(title, prompt string) string {
	title = strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
	if title == "" {
		title = strings.Join(strings.Fields(strings.TrimSpace(prompt)), " ")
	}
	if title == "" {
		return "Scheduled task"
	}
	runes := []rune(title)
	if len(runes) > 60 {
		return strings.TrimSpace(string(runes[:60])) + "…"
	}
	return title
}

func taskSessionTitle(task Task) string {
	title := deriveTaskTitle(task.Title, task.Prompt)
	return "⏰ " + title
}
