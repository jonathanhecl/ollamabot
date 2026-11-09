package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
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

// SchedulerService defines the interface implemented by the scheduler manager.
type SchedulerService interface {
	CreateScheduledTask(spec ScheduledTaskSpec) (ScheduledTaskInfo, error)
	UpdateScheduledTask(id string, patch ScheduledTaskPatch) (ScheduledTaskInfo, error)
	DeleteScheduledTask(id string) error
	ListTasks(channel string, sessionID string, includeCompleted bool) []ScheduledTaskInfo
	CancelTask(id string) error
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
			Description: "List every scheduled reminder and autonomous task, with its ID, schedule, channels and last result state. Use it before editing or deleting so you reference the right ID.",
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
}

// SetSchedulerManager is an alias for SetSchedulerService for convenience.
func (r *Registry) SetSchedulerManager(sm SchedulerService) {
	r.SetSchedulerService(sm)
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
