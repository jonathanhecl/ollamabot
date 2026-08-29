package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/mcp"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
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

func (m *Manager) notify(task Task, message string) {
	m.mu.RLock()
	fn, ok := m.notifiers[task.Channel]
	m.mu.RUnlock()

	if ok && fn != nil {
		go func() {
			if err := fn(task, message); err != nil {
				log.Printf("[scheduler] Notification failed for task %s on channel %s: %v", task.ID, task.Channel, err)
			}
		}()
	} else {
		log.Printf("[scheduler] No notifier registered for channel %q (task %s)", task.Channel, task.ID)
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

// Load reads tasks from disk.
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
		m.tasks[t.ID] = t
	}
	return nil
}

// Save writes tasks to disk.
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
	return os.WriteFile(m.storagePath, data, 0644)
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
		task.Channel = "telegram"
	}

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
		ID:           t.ID,
		Type:         string(t.Type),
		ScheduleType: string(t.ScheduleType),
		Prompt:       t.Prompt,
		CronExpr:     t.CronExpr,
		IntervalStr:  t.IntervalStr,
		NextRunAt:    t.NextRunAt,
		Status:       string(t.Status),
	}
}

// CreateReminder is a helper to schedule a simple text alert.
func (m *Manager) CreateReminder(channel string, sessionID string, targetChatID int64, text string, when string) (tools.ScheduledTaskInfo, error) {
	now := time.Now()
	schedType, nextRun, expr, err := ParseSchedule(when, now)
	if err != nil {
		return tools.ScheduledTaskInfo{}, fmt.Errorf("invalid schedule format %q: %w", when, err)
	}

	task := Task{
		ID:           GenerateTaskID(),
		Type:         TaskTypeAlert,
		ScheduleType: schedType,
		Prompt:       text,
		Channel:      channel,
		SessionID:    sessionID,
		TargetChatID: targetChatID,
		CreatedAt:    now,
		NextRunAt:    nextRun,
		Status:       TaskStatusPending,
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

// CreateTask is a helper to schedule an autonomous agent execution.
func (m *Manager) CreateTask(channel string, sessionID string, targetChatID int64, instruction string, when string, isAgentTask bool) (tools.ScheduledTaskInfo, error) {
	now := time.Now()
	schedType, nextRun, expr, err := ParseSchedule(when, now)
	if err != nil {
		return tools.ScheduledTaskInfo{}, fmt.Errorf("invalid schedule format %q: %w", when, err)
	}

	tType := TaskTypeAlert
	if isAgentTask {
		tType = TaskTypeAgentTask
	}

	task := Task{
		ID:           GenerateTaskID(),
		Type:         tType,
		ScheduleType: schedType,
		Prompt:       instruction,
		Channel:      channel,
		SessionID:    sessionID,
		TargetChatID: targetChatID,
		CreatedAt:    now,
		NextRunAt:    nextRun,
		Status:       TaskStatusPending,
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []tools.ScheduledTaskInfo
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
		m.notify(task, msg)

		m.mu.Lock()
		task.LastRunAt = &now
		task.RunCount++
		m.advanceOrComplete(&task, now)
		m.tasks[task.ID] = task
		m.mu.Unlock()

		_ = m.Save()
		return
	}

	// TaskTypeAgentTask: Run autonomous turn using agent loop
	releaseSlot := sessions.TryAcquireBackgroundSlot()
	if releaseSlot == nil {
		log.Printf("[scheduler] Background slot busy, deferring agent task %s", task.ID)
		return
	}
	defer releaseSlot()

	log.Printf("[scheduler] Executing autonomous task %s: %q", task.ID, task.Prompt)

	model := config.ResolveModel(m.config(), config.ModelRoleMain)
	if strings.TrimSpace(model) == "" {
		model = m.config().OllamaDefaultModel
	}

	registry := tools.NewRegistry(m.config().WebSearchEnabled, m.config().Workspace, m.memoryStore, m.client, m.config().OllamaModelEmbed, tools.SearchConfig{
		Providers:    m.config().SearchProviders,
		BraveAPIKey:  m.config().BraveSearchAPIKey,
		TavilyAPIKey: m.config().TavilyAPIKey,
	})
	registry.SetApprovalPolicy(tools.ApprovalPolicyAutonomous)
	registry.SetMCPManager(m.mcpMgr)
	registry.SetSessionStore(m.sessionStore)
	registry.SetSessionID(task.SessionID)

	a := agent.NewAgent(m.cfgMgr, m.client, registry)
	systemPrompt := `You are OllamaBot executing a scheduled background task.
Use your available tools (such as web search, MCP tools, memory, etc.) to accomplish the goal thoroughly.
When finished, provide a clear, concise, and helpful final summary report for the user. Do not return empty messages.`

	messages := []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Execute scheduled task: %s", task.Prompt)},
	}

	runCtx, runCancel := agent.SubagentContext(ctx, m.config())
	finalHistory, err := a.Run(runCtx, model, messages, m.config().OllamaThinkEnabled, &noopStreamHandler{})
	runCancel()

	if err != nil {
		log.Printf("[scheduler] Agent task %s failed: %v", task.ID, err)
		m.mu.Lock()
		task.LastRunAt = &now
		task.LastError = err.Error()
		if task.ScheduleType == ScheduleTypeOnce {
			task.Status = TaskStatusFailed
		} else {
			m.advanceOrComplete(&task, now)
		}
		m.tasks[task.ID] = task
		m.mu.Unlock()
		_ = m.Save()

		m.notify(task, fmt.Sprintf("⚠️ **Scheduled Task Failed** (%s):\nError: %v", task.Prompt, err))
		return
	}

	// Extract response
	var response string
	for i := len(finalHistory) - 1; i >= 0; i-- {
		if finalHistory[i].Role == "assistant" && strings.TrimSpace(finalHistory[i].Content) != "" {
			response = strings.TrimSpace(finalHistory[i].Content)
			break
		}
	}
	if response == "" {
		response = "Task completed successfully with no additional output."
	}

	// Notify user
	notifyMsg := fmt.Sprintf("📋 **Scheduled Task Report**\n**Task**: %s\n\n%s", task.Prompt, response)
	m.notify(task, notifyMsg)

	// Save to session history if sessionID is set
	if m.sessionStore != nil && task.SessionID != "" {
		if sess, err := m.sessionStore.Get(task.SessionID); err == nil {
			asstMsg := sessions.RawMsg{
				Role:    "assistant",
				Content: fmt.Sprintf("[Scheduled Task Result for %q]\n\n%s", task.Prompt, response),
			}
			if raw, err := json.Marshal(asstMsg); err == nil {
				sess.Messages = append(sess.Messages, raw)
				_ = m.sessionStore.Save(sess)
				sessions.NotifyUpdate(task.SessionID)
			}
		}
	}

	m.mu.Lock()
	task.LastRunAt = &now
	task.RunCount++
	task.LastError = ""
	m.advanceOrComplete(&task, now)
	m.tasks[task.ID] = task
	m.mu.Unlock()

	_ = m.Save()
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

type noopStreamHandler struct{}

func (noopStreamHandler) OnThinking(string)                                {}
func (noopStreamHandler) OnContent(string)                                 {}
func (noopStreamHandler) OnToolCall(ollama.ToolCall)                       {}
func (noopStreamHandler) OnToolStart(string, any, string)                  {}
func (noopStreamHandler) OnToolResult(string, string, string)              {}
func (noopStreamHandler) OnMediaPreProcessing(string)                      {}
func (noopStreamHandler) OnDone(ollama.ChatResponse)                       {}
func (noopStreamHandler) OnEvent(string, any)                              {}
func (noopStreamHandler) OnContextOptimizationStart(int, float64)          {}
func (noopStreamHandler) OnContextOptimizationEnd(int, float64, float64)   {}
func (noopStreamHandler) OnContextOptimized([]ollama.Message, string, int) {}
