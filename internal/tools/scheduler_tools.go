package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// Bounds for how long schedule_run waits for a forced run before answering.
const (
	defaultManualRunWaitSeconds = 90
	maxManualRunWaitSeconds     = 300
)

// ScheduledTaskInfo is a light data transfer struct returned by SchedulerService.
type ScheduledTaskInfo struct {
	ID              string
	Type            string // "alert" or "agent_task"
	ScheduleType    string // "once", "interval", "cron"
	Title           string
	Prompt          string
	CronExpr        string
	IntervalStr     string
	Channel         string // origin channel
	OriginSessionID string
	SessionID       string // dedicated run session for agent tasks
	NotifyChannels  []string
	NotifyMode      string // "always" or "on_alert"
	LastObservation string
	LastAlert       string
	LastAlertAt     *time.Time
	NextRunAt       time.Time
	LastRunAt       *time.Time
	Status          string
	LastError       string
	RunCount        int
}

// ScheduledTaskSpec describes a new reminder or background agent task.
type ScheduledTaskSpec struct {
	Channel         string
	OriginSessionID string
	TargetChatID    int64
	Title           string
	Type            string // "alert" or "agent_task"
	Prompt          string
	When            string
	NotifyChannels  []string
	NotifyMode      string // "always" or "on_alert" (silent monitoring)
}

// ScheduledTaskPatch edits an existing task. Empty strings and a nil channel
// slice mean "leave unchanged".
type ScheduledTaskPatch struct {
	Title          string
	Prompt         string
	When           string
	NotifyChannels []string
	NotifyMode     string // "always" or "on_alert"; empty means unchanged
	Status         string // "paused" or "pending"
}

// ManualRunOutcome reports what a forced (on-demand) run produced.
type ManualRunOutcome struct {
	Info     ScheduledTaskInfo
	Answer   string
	Alerts   []string
	Err      string
	Duration time.Duration
}

// SchedulerService defines the interface implemented by the scheduler manager.
type SchedulerService interface {
	CreateScheduledTask(spec ScheduledTaskSpec) (ScheduledTaskInfo, error)
	UpdateScheduledTask(id string, patch ScheduledTaskPatch) (ScheduledTaskInfo, error)
	DeleteScheduledTask(id string) error
	ListTasks(channel string, sessionID string, includeCompleted bool) []ScheduledTaskInfo
	RelatedTasks(query string, queryEmbedding []float64, limit int) []ScheduledTaskInfo
	CancelTask(id string) error
	RunScheduledTaskNow(id string) (<-chan ManualRunOutcome, ScheduledTaskInfo, error)
}

func (r *Registry) SetSchedulerService(sm SchedulerService) {
	r.schedulerService = sm
	if sm == nil {
		return
	}

	r.enabled["schedule_reminder"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_reminder",
			Description: "Schedule a fixed reminder or alert for the user. Supports relative delays ('in 15m', '2h', '1d'), absolute times ('15:30', '2026-08-29 18:00'), intervals ('every 30m'), or cron expressions ('0 9 * * *', '@daily'). Before calling this, make sure the user confirmed the exact time; if the time is ambiguous, ask with ask_clarification first. Creation always asks the user for approval, so the reminder is only created after they confirm.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Short human label for the task list (e.g. 'Bitcoin morning check').",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "The reminder message or alert text to deliver to the user.",
					},
					"when": map[string]any{
						"type":        "string",
						"description": "When to trigger (e.g. 'in 20m', '18:00', 'every 2h', '0 9 * * *', '@daily').",
					},
					"notify_channels": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []string{"web", "telegram"}},
						"description": "Channels that receive the reminder. Defaults to web and telegram when available.",
					},
				},
				"required": []string{"text", "when"},
			},
		},
	})

	r.enabled["schedule_task"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_task",
			Description: "Schedule a proactive autonomous task. At the scheduled time the agent runs in the background with its normal tools (web search, MCP, memory, files) and reports the result to the user. Use this for requests like 'every morning tell me how bitcoin is doing', or 'every 5 hours check for news about X and only tell me if something happens' with notify_mode 'on_alert'. Before calling this, confirm the exact schedule with the user (ask_clarification if the hour is ambiguous). Creation always asks the user for approval.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Short human label for the task list (e.g. 'Bitcoin morning check').",
					},
					"instruction": map[string]any{
						"type":        "string",
						"description": "The detailed prompt or instruction for the agent to execute with tools. Write it as a self-contained request.",
					},
					"when": map[string]any{
						"type":        "string",
						"description": "When or how often to execute (e.g. '0 9 * * *', 'every 24h', 'in 1h', '@daily').",
					},
					"notify_channels": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []string{"web", "telegram"}},
						"description": "Channels that receive the result. Defaults to web and telegram when available.",
					},
					"notify_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"always", "on_alert"},
						"description": "Use 'on_alert' for silent monitoring: the agent only messages the user when the condition is met or something materially new appears, by calling send_scheduled_alert. Use 'always' (default) to report on every run.",
					},
				},
				"required": []string{"instruction", "when"},
			},
		},
	})

	r.enabled["schedule_list"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_list",
			Description: "List every scheduled reminder and autonomous task, with its ID, schedule, channels, status, last run and last observation. Use it before editing or deleting so you reference the right ID, and to answer from recent task data without re-running the task. The last observation includes when it was measured. To execute one immediately, pass its ID to schedule_run.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"include_completed": map[string]any{
						"type":        "boolean",
						"description": "Set to true to include completed or cancelled tasks (defaults to false).",
					},
				},
			},
		},
	})

	r.enabled["schedule_update"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_update",
			Description: "Edit an existing scheduled task: change its title, instruction, schedule or delivery channels, or pause/resume it. Do not ask for permission first when the user requested the change; apply it and clearly report what changed in your reply.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task to update (e.g. 'rem_1a2b3c4d').",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "New short label (omit to keep the current one).",
					},
					"instruction": map[string]any{
						"type":        "string",
						"description": "New instruction or reminder text (omit to keep the current one).",
					},
					"when": map[string]any{
						"type":        "string",
						"description": "New schedule (omit to keep the current one).",
					},
					"notify_channels": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []string{"web", "telegram"}},
						"description": "New delivery channels (omit to keep the current ones).",
					},
					"notify_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"always", "on_alert"},
						"description": "New notification mode: 'always' reports every run, 'on_alert' only when the agent decides there is news (omit to keep the current one).",
					},
					"status": map[string]any{
						"type":        "string",
						"enum":        []string{"paused", "pending"},
						"description": "Use 'paused' to pause the task or 'pending' to resume it.",
					},
				},
				"required": []string{"task_id"},
			},
		},
	})

	r.enabled["schedule_delete"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_delete",
			Description: "Delete a scheduled task permanently. Do not ask for permission first when the user asked to remove it (or when the task is obsolete); delete it and clearly report the deletion in your reply.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task to delete (e.g. 'rem_1a2b3c4d').",
					},
				},
				"required": []string{"task_id"},
			},
		},
	})

	r.enabled["schedule_cancel"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_cancel",
			Description: "Cancel a scheduled reminder or task without removing its record. Prefer schedule_delete when the user wants it gone from the list.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task to cancel (e.g. 'rem_1a2b3c4d').",
					},
				},
				"required": []string{"task_id"},
			},
		},
	})

	r.enabled["schedule_run"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_run",
			Description: "Run a scheduled reminder or task right now, ignoring its schedule and its paused/completed state. Use it whenever the user asks to execute, test or verify a scheduled task immediately ('run it now', 'does it work?', 'show me how it would work'): the run is the real one, with the same instruction, tools, model and delivery channels, so whatever arrives is what the user would get at the scheduled time. The next scheduled run stays exactly where it was, and the run is recorded as the last observation. Without wait_seconds it returns immediately and the result arrives through the task's channels; with wait_seconds it also returns the actual output in the same turn. Do not use it to create or change a task.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task to run (e.g. 'rem_1a2b3c4d'). Use schedule_list first if you do not know it.",
					},
					"wait_seconds": map[string]any{
						"type":        "integer",
						"description": "How long to wait for the run to finish and return its output (default 90, max 300). Use 0 to start it and answer immediately.",
					},
				},
				"required": []string{"task_id"},
			},
		},
	})
}

// SetSchedulerManager is an alias for SetSchedulerService for convenience.
func (r *Registry) SetSchedulerManager(sm SchedulerService) {
	r.SetSchedulerService(sm)
}

// SchedulerEnabled reports whether scheduled task tools are available.
func (r *Registry) SchedulerEnabled() bool {
	return r != nil && r.schedulerService != nil
}

// RelatedScheduledTasksContext builds the compact prompt block with the tasks
// related to the current question. It relies on task embeddings computed by the
// scheduler and only includes the best matches.
func (r *Registry) RelatedScheduledTasksContext(query string, queryEmbedding []float64, limit int) string {
	if r == nil || r.schedulerService == nil {
		return ""
	}
	tasks := r.schedulerService.RelatedTasks(query, queryEmbedding, limit)
	if len(tasks) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Related Scheduled Task Observations\n")
	sb.WriteString("These background tasks match the user's question. Use their latest observation when it is recent enough and state when it was measured; refresh with tools if the user asks for the current value.\n")
	for _, task := range tasks {
		title := strings.TrimSpace(task.Title)
		if title == "" {
			title = strings.TrimSpace(task.Prompt)
		}
		fmt.Fprintf(&sb, "- **%s**", title)
		if task.Status == "paused" {
			sb.WriteString(" (paused)")
		}
		if task.LastRunAt != nil {
			fmt.Fprintf(&sb, " (measured %s)", task.LastRunAt.Format("2006-01-02 15:04"))
		}
		if strings.TrimSpace(task.LastObservation) != "" {
			fmt.Fprintf(&sb, ": %s", truncateRunes(task.LastObservation, 500))
		} else {
			sb.WriteString(": no observation yet")
		}
		if condition := strings.TrimSpace(task.Prompt); condition != "" && !strings.Contains(condition, title) {
			fmt.Fprintf(&sb, " [condition: %s]", truncateRunes(condition, 200))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// SetScheduledAlertHandler enables send_scheduled_alert inside silent
// monitoring runs. The scheduler installs a collector and delivers whatever
// the model decides to report.
func (r *Registry) SetScheduledAlertHandler(h func(string)) {
	r.scheduledAlertHandler = h
	if h == nil {
		return
	}

	r.enabled["send_scheduled_alert"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "send_scheduled_alert",
			Description: "Send the user a proactive alert for a silent monitoring task. Call this ONLY when the condition described in the task is met or something materially new appeared since the previous observation. If nothing new happened, do not call it and finish normally: the user will not be notified.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "The concise user-facing alert. Say what changed and why it matters; no internal reasoning.",
					},
				},
				"required": []string{"message"},
			},
		},
	})
}

func (r *Registry) SetChannel(channel string) {
	r.channel = channel
}

// Channel reports the transport the current turn came from ("web", "telegram",
// "device"...). The agent uses it to adapt its answer to the channel.
func (r *Registry) Channel() string {
	if r == nil {
		return ""
	}
	return r.channel
}

func (r *Registry) SetTargetChatID(chatID int64) {
	r.targetChatID = chatID
}

func scheduleNotifyChannels(args map[string]any) []string {
	raw, ok := args["notify_channels"].([]any)
	if !ok {
		if list, ok := args["notify_channels"].([]string); ok {
			return list
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func scheduleNotifyMode(args map[string]any) string {
	mode, _ := args["notify_mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "always" && mode != "on_alert" {
		return ""
	}
	return mode
}

func (r *Registry) executeSendScheduledAlert(args map[string]any) (string, error) {
	if r.scheduledAlertHandler == nil {
		return "", fmt.Errorf("scheduled alerts are not available in this run")
	}
	message, _ := args["message"].(string)
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("missing alert message")
	}
	r.scheduledAlertHandler(strings.TrimSpace(message))
	return "Alert queued. The user will receive it when this run finishes.", nil
}

func (r *Registry) executeScheduleReminder(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	text, _ := args["text"].(string)
	when, _ := args["when"].(string)
	title, _ := args["title"].(string)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("missing reminder text")
	}
	if strings.TrimSpace(when) == "" {
		return "", fmt.Errorf("missing reminder when")
	}

	task, err := r.schedulerService.CreateScheduledTask(ScheduledTaskSpec{
		Channel:         r.channel,
		OriginSessionID: r.sessionID,
		TargetChatID:    r.targetChatID,
		Title:           title,
		Type:            "alert",
		Prompt:          text,
		When:            when,
		NotifyChannels:  scheduleNotifyChannels(args),
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Reminder scheduled successfully!\nID: %s\nTitle: %s\nTarget: %s\nText: %s\nChannels: %s",
		task.ID, task.Title, describeSchedule(task), task.Prompt, strings.Join(task.NotifyChannels, ", ")), nil
}

func (r *Registry) executeScheduleTask(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	instruction, _ := args["instruction"].(string)
	when, _ := args["when"].(string)
	title, _ := args["title"].(string)
	if strings.TrimSpace(instruction) == "" {
		return "", fmt.Errorf("missing task instruction")
	}
	if strings.TrimSpace(when) == "" {
		return "", fmt.Errorf("missing task when")
	}

	task, err := r.schedulerService.CreateScheduledTask(ScheduledTaskSpec{
		Channel:         r.channel,
		OriginSessionID: r.sessionID,
		TargetChatID:    r.targetChatID,
		Title:           title,
		Type:            "agent_task",
		Prompt:          instruction,
		When:            when,
		NotifyChannels:  scheduleNotifyChannels(args),
		NotifyMode:      scheduleNotifyMode(args),
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Autonomous background task scheduled successfully!\nID: %s\nTitle: %s\nTarget: %s\nInstruction: %s\nChannels: %s\nNotify mode: %s",
		task.ID, task.Title, describeSchedule(task), task.Prompt, strings.Join(task.NotifyChannels, ", "), notifyModeLabel(task.NotifyMode)), nil
}

func describeSchedule(t ScheduledTaskInfo) string {
	switch t.ScheduleType {
	case "cron":
		return fmt.Sprintf("Cron %q (next: %s)", t.CronExpr, t.NextRunAt.Format("2006-01-02 15:04:05"))
	case "interval":
		return fmt.Sprintf("Every %s (next: %s)", t.IntervalStr, t.NextRunAt.Format("2006-01-02 15:04:05"))
	default:
		return t.NextRunAt.Format("2006-01-02 15:04:05")
	}
}

func notifyModeLabel(mode string) string {
	if mode == "on_alert" {
		return "only when there is something new"
	}
	return "every run"
}

func (r *Registry) executeScheduleList(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	includeCompleted := false
	if v, ok := args["include_completed"].(bool); ok {
		includeCompleted = v
	}

	// List every channel: tasks created from the web chat must be visible from
	// Telegram and vice versa.
	tasks := r.schedulerService.ListTasks("", "", includeCompleted)
	if len(tasks) == 0 {
		return "No active reminders or scheduled tasks found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d scheduled task(s):\n\n", len(tasks)))
	now := time.Now()
	for idx, t := range tasks {
		typeLabel := "Reminder"
		if t.Type == "agent_task" {
			typeLabel = "Autonomous Task"
		}
		timeInfo := t.NextRunAt.Format("2006-01-02 15:04:05")
		if t.ScheduleType == "cron" {
			timeInfo = fmt.Sprintf("Cron `%s` (Next: %s)", t.CronExpr, t.NextRunAt.Format("15:04:05"))
		} else if t.ScheduleType == "interval" {
			timeInfo = fmt.Sprintf("Interval `%s` (Next: %s)", t.IntervalStr, t.NextRunAt.Format("15:04:05"))
		} else if t.NextRunAt.After(now) {
			timeInfo = fmt.Sprintf("%s (in %s)", timeInfo, t.NextRunAt.Sub(now).Round(time.Second))
		}

		title := t.Title
		if title == "" {
			title = t.Prompt
		}
		fmt.Fprintf(&sb, "%d. **[%s]** `%s` — %s\n", idx+1, typeLabel, t.ID, t.Status)
		fmt.Fprintf(&sb, "   - **Title**: %s\n", title)
		fmt.Fprintf(&sb, "   - **Content**: %s\n", t.Prompt)
		fmt.Fprintf(&sb, "   - **Schedule**: %s\n", timeInfo)
		if len(t.NotifyChannels) > 0 {
			fmt.Fprintf(&sb, "   - **Channels**: %s\n", strings.Join(t.NotifyChannels, ", "))
		}
		if t.NotifyMode == "on_alert" {
			sb.WriteString("   - **Notify**: only when there is something new (silent monitoring)\n")
		}
		if t.LastRunAt != nil {
			fmt.Fprintf(&sb, "   - **Last run**: %s\n", t.LastRunAt.Format("2006-01-02 15:04"))
		}
		if t.LastObservation != "" {
			fmt.Fprintf(&sb, "   - **Last observation**: %s\n", truncateRunes(t.LastObservation, 700))
		}
		if t.LastAlert != "" {
			when := ""
			if t.LastAlertAt != nil {
				when = " (" + t.LastAlertAt.Format("2006-01-02 15:04") + ")"
			}
			fmt.Fprintf(&sb, "   - **Last alert%s**: %s\n", when, truncateRunes(t.LastAlert, 400))
		}
		if t.LastError != "" {
			fmt.Fprintf(&sb, "   - **Last error**: %s\n", t.LastError)
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()), nil
}

func (r *Registry) executeScheduleUpdate(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("missing task_id")
	}
	title, _ := args["title"].(string)
	instruction, _ := args["instruction"].(string)
	when, _ := args["when"].(string)
	status, _ := args["status"].(string)

	patch := ScheduledTaskPatch{
		Title:          strings.TrimSpace(title),
		Prompt:         strings.TrimSpace(instruction),
		When:           strings.TrimSpace(when),
		NotifyChannels: scheduleNotifyChannels(args),
		NotifyMode:     scheduleNotifyMode(args),
		Status:         strings.TrimSpace(strings.ToLower(status)),
	}
	if patch.Title == "" && patch.Prompt == "" && patch.When == "" && patch.NotifyChannels == nil && patch.NotifyMode == "" && patch.Status == "" {
		return "", fmt.Errorf("nothing to update: provide title, instruction, when, notify_channels, notify_mode or status")
	}

	task, err := r.schedulerService.UpdateScheduledTask(taskID, patch)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled task %q updated.\nTitle: %s\nStatus: %s\nSchedule: %s\nChannels: %s\nNotify mode: %s\n\nReport this change to the user.",
		task.ID, task.Title, task.Status, describeSchedule(task), strings.Join(task.NotifyChannels, ", "), notifyModeLabel(task.NotifyMode)), nil
}

func (r *Registry) executeScheduleDelete(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("missing task_id")
	}

	if err := r.schedulerService.DeleteScheduledTask(taskID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled task %q was deleted permanently. Report this deletion to the user.", taskID), nil
}

func (r *Registry) executeScheduleCancel(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("missing task_id")
	}

	if err := r.schedulerService.CancelTask(taskID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled task %q was cancelled. Report this change to the user.", taskID), nil
}

func (r *Registry) executeScheduleRun(ctx context.Context, args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("missing task_id")
	}

	waitSeconds := defaultManualRunWaitSeconds
	if v, ok := args["wait_seconds"]; ok && v != nil {
		waitSeconds = toInt(v)
	}
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	if waitSeconds > maxManualRunWaitSeconds {
		waitSeconds = maxManualRunWaitSeconds
	}

	outcomeCh, info, err := r.schedulerService.RunScheduledTaskNow(strings.TrimSpace(taskID))
	if err != nil {
		return "", err
	}

	channels := strings.Join(info.NotifyChannels, ", ")
	if channels == "" {
		channels = info.Channel
	}
	nextRun := "none"
	if !info.NextRunAt.IsZero() {
		nextRun = info.NextRunAt.Format("2006-01-02 15:04")
	}

	if waitSeconds > 0 {
		timer := time.NewTimer(time.Duration(waitSeconds) * time.Second)
		defer timer.Stop()
		select {
		case outcome := <-outcomeCh:
			return describeManualRun(info, outcome, channels, nextRun), nil
		case <-timer.C:
		case <-ctx.Done():
		}
	}

	title := strings.TrimSpace(info.Title)
	if title == "" {
		title = info.Prompt
	}
	if waitSeconds == 0 {
		return fmt.Sprintf("Forced run of task %q started. It is running now with its real tools; the output arrives on: %s.\nFull trace: session %s.\nThe schedule was not changed (next run: %s). Tell the user it is running and where the result will appear.",
			title, channels, info.SessionID, nextRun), nil
	}
	return fmt.Sprintf("Forced run of task %q started and had not finished after %ds.\nType: %s\nIt keeps running with its real tools (it may still be waiting for the model to be free); the output arrives on: %s.\nFull trace: session %s.\nThe schedule was not changed (next run: %s). Tell the user it is running and where the result will appear.",
		title, waitSeconds, manualRunTypeLabel(info), channels, info.SessionID, nextRun), nil
}

// manualRunTypeLabel names what a task is in one word for the tool output.
func manualRunTypeLabel(info ScheduledTaskInfo) string {
	if info.Type == "agent_task" {
		return "autonomous agent task"
	}
	return "reminder"
}

// describeManualRun renders the real outcome of a forced run: what was
// delivered, and what the user would see at the scheduled time.
func describeManualRun(started ScheduledTaskInfo, outcome ManualRunOutcome, channels, nextRun string) string {
	info := outcome.Info
	if info.ID == "" {
		info = started
	}
	title := strings.TrimSpace(info.Title)
	if title == "" {
		title = info.Prompt
	}

	var sb strings.Builder
	if outcome.Err != "" {
		fmt.Fprintf(&sb, "The forced run of task %q FAILED after %s.\nError: %s\n", title, outcome.Duration.Round(time.Second), outcome.Err)
		sb.WriteString("A scheduled run would fail the same way. The failure notice was sent to: " + channels + ".\n")
		fmt.Fprintf(&sb, "The schedule was not changed (next run: %s). Status stays %q. Explain the failure and what to fix.",
			nextRun, info.Status)
		return sb.String()
	}

	fmt.Fprintf(&sb, "The forced run of task %q finished in %s. This was a real run: same instruction, same tools, same model, same channels.\n",
		title, outcome.Duration.Round(time.Second))
	fmt.Fprintf(&sb, "Type: %s\nChannels: %s\nLog session: %s\n\n", manualRunTypeLabel(info), channels, info.SessionID)

	answer := strings.TrimSpace(outcome.Answer)
	switch {
	case info.Type == "alert":
		sb.WriteString("The user received this reminder:\n")
		sb.WriteString(truncateRunes(answer, 2000))
		sb.WriteString("\n")
	case info.NotifyMode == "on_alert" && len(outcome.Alerts) == 0:
		sb.WriteString("Silent monitoring (only when there is something new): the run decided there was nothing to report, so the user would NOT have received a message.\n")
		if answer != "" {
			sb.WriteString("\nWhat the run found (stored as the last observation):\n")
			sb.WriteString(truncateRunes(answer, 2000))
			sb.WriteString("\n")
		}
	case info.NotifyMode == "on_alert":
		fmt.Fprintf(&sb, "Silent monitoring alerted the user with:\n%s\n", truncateRunes(strings.Join(outcome.Alerts, "\n\n"), 2000))
		if answer != "" {
			sb.WriteString("\nRun report:\n")
			sb.WriteString(truncateRunes(answer, 2000))
			sb.WriteString("\n")
		}
	default:
		sb.WriteString("The user received this report:\n")
		if answer == "" {
			answer = "Task completed successfully with no additional output."
		}
		sb.WriteString(truncateRunes(answer, 2500))
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb, "\nThe schedule was not changed (next run: %s), status stays %q. Summarize the outcome for the user and say what would arrive at the scheduled time.",
		nextRun, info.Status)
	return sb.String()
}
