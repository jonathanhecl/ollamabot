package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// ScheduledTaskInfo is a light data transfer struct returned by SchedulerService.
type ScheduledTaskInfo struct {
	ID           string
	Type         string // "alert" or "agent_task"
	ScheduleType string // "once", "interval", "cron"
	Prompt       string
	CronExpr     string
	IntervalStr  string
	NextRunAt    time.Time
	Status       string
}

// SchedulerService defines the interface implemented by the scheduler manager.
type SchedulerService interface {
	CreateReminder(channel string, sessionID string, targetChatID int64, text string, when string) (ScheduledTaskInfo, error)
	CreateTask(channel string, sessionID string, targetChatID int64, instruction string, when string, isAgentTask bool) (ScheduledTaskInfo, error)
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
			Description: "Schedule a reminder or alert for the user. Supports relative delays ('in 15m', '2h', '1d'), absolute times ('15:30', '2026-08-29 18:00'), intervals ('every 30m', 'every 2h'), or cron expressions ('0 9 * * *', '@daily'). When the scheduled time arrives, the reminder text is delivered directly to the user.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The reminder message or alert text to deliver to the user.",
					},
					"when": map[string]any{
						"type":        "string",
						"description": "When to trigger (e.g. 'in 20m', '18:00', 'every 2h', '0 9 * * *', '@daily').",
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
			Description: "Schedule an autonomous background task for the agent to execute at a specific time or recurring schedule. The agent will run in the background with access to tools (like web search, MCP tools, etc.) to perform the instruction and report back the result to the user.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"instruction": map[string]any{
						"type":        "string",
						"description": "The detailed prompt or instruction for the agent to execute with tools.",
					},
					"when": map[string]any{
						"type":        "string",
						"description": "When or how often to execute (e.g. '0 9 * * *', 'every 24h', 'in 1h', '@daily').",
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
			Description: "List all active, upcoming, and recurring scheduled reminders and tasks.",
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

	r.enabled["schedule_cancel"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "schedule_cancel",
			Description: "Cancel or remove a scheduled reminder or task by its ID.",
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

func (r *Registry) SetChannel(channel string) {
	r.channel = channel
}

func (r *Registry) SetTargetChatID(chatID int64) {
	r.targetChatID = chatID
}

func (r *Registry) executeScheduleReminder(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	text, _ := args["text"].(string)
	when, _ := args["when"].(string)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("missing reminder text")
	}
	if strings.TrimSpace(when) == "" {
		return "", fmt.Errorf("missing reminder when")
	}

	task, err := r.schedulerService.CreateReminder(r.channel, r.sessionID, r.targetChatID, text, when)
	if err != nil {
		return "", err
	}

	scheduleDesc := task.NextRunAt.Format("2006-01-02 15:04:05")
	if task.ScheduleType == "cron" {
		scheduleDesc = fmt.Sprintf("Cron %q (next: %s)", task.CronExpr, task.NextRunAt.Format("2006-01-02 15:04:05"))
	} else if task.ScheduleType == "interval" {
		scheduleDesc = fmt.Sprintf("Every %s (next: %s)", task.IntervalStr, task.NextRunAt.Format("2006-01-02 15:04:05"))
	}

	return fmt.Sprintf("Reminder scheduled successfully!\nID: %s\nTarget: %s\nText: %s", task.ID, scheduleDesc, task.Prompt), nil
}

func (r *Registry) executeScheduleTask(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	instruction, _ := args["instruction"].(string)
	when, _ := args["when"].(string)
	if strings.TrimSpace(instruction) == "" {
		return "", fmt.Errorf("missing task instruction")
	}
	if strings.TrimSpace(when) == "" {
		return "", fmt.Errorf("missing task when")
	}

	task, err := r.schedulerService.CreateTask(r.channel, r.sessionID, r.targetChatID, instruction, when, true)
	if err != nil {
		return "", err
	}

	scheduleDesc := task.NextRunAt.Format("2006-01-02 15:04:05")
	if task.ScheduleType == "cron" {
		scheduleDesc = fmt.Sprintf("Cron %q (next: %s)", task.CronExpr, task.NextRunAt.Format("2006-01-02 15:04:05"))
	} else if task.ScheduleType == "interval" {
		scheduleDesc = fmt.Sprintf("Every %s (next: %s)", task.IntervalStr, task.NextRunAt.Format("2006-01-02 15:04:05"))
	}

	return fmt.Sprintf("Autonomous background task scheduled successfully!\nID: %s\nTarget: %s\nInstruction: %s", task.ID, scheduleDesc, task.Prompt), nil
}

func (r *Registry) executeScheduleList(args map[string]any) (string, error) {
	if r.schedulerService == nil {
		return "", fmt.Errorf("scheduler service is not available")
	}
	includeCompleted := false
	if v, ok := args["include_completed"].(bool); ok {
		includeCompleted = v
	}

	tasks := r.schedulerService.ListTasks(r.channel, "", includeCompleted)
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

		fmt.Fprintf(&sb, "%d. **[%s]** `%s` — %s\n", idx+1, typeLabel, t.ID, t.Status)
		fmt.Fprintf(&sb, "   - **Content**: %s\n", t.Prompt)
		fmt.Fprintf(&sb, "   - **Schedule**: %s\n\n", timeInfo)
	}

	return strings.TrimSpace(sb.String()), nil
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
	return fmt.Sprintf("Scheduled task %q was successfully cancelled.", taskID), nil
}
