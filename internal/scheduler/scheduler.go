package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type TaskType string

const (
	TaskTypeAlert     TaskType = "alert"      // Simple text notification delivered to the user
	TaskTypeAgentTask TaskType = "agent_task" // Autonomous agent execution with tools
)

type ScheduleType string

const (
	ScheduleTypeOnce     ScheduleType = "once"
	ScheduleTypeInterval ScheduleType = "interval"
	ScheduleTypeCron     ScheduleType = "cron"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task represents a scheduled reminder or background agent task.
type Task struct {
	ID           string       `json:"id"`
	Type         TaskType     `json:"type"`
	ScheduleType ScheduleType `json:"schedule_type"`
	Prompt       string       `json:"prompt"`
	CronExpr     string       `json:"cron_expr,omitempty"`
	IntervalStr  string       `json:"interval_str,omitempty"`
	Channel      string       `json:"channel"` // "telegram" or "web"
	SessionID    string       `json:"session_id,omitempty"`
	TargetChatID int64        `json:"target_chat_id,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	NextRunAt    time.Time    `json:"next_run_at"`
	LastRunAt    *time.Time   `json:"last_run_at,omitempty"`
	Status       TaskStatus   `json:"status"`
	LastError    string       `json:"last_error,omitempty"`
	RunCount     int          `json:"run_count"`
}

func GenerateTaskID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("rem_%d", time.Now().UnixNano()%1000000)
	}
	return "rem_" + hex.EncodeToString(b)
}

// ParseSchedule parses a time specification into a target time, interval, or cron expression.
func ParseSchedule(input string, now time.Time) (ScheduleType, time.Time, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", time.Time{}, "", fmt.Errorf("schedule expression cannot be empty")
	}

	lower := strings.ToLower(input)

	// Check standard cron aliases
	if lower == "@daily" || lower == "daily" || lower == "every day" {
		next, err := NextCronTime("0 9 * * *", now) // default 9:00 AM for daily
		return ScheduleTypeCron, next, "0 9 * * *", err
	}
	if lower == "@hourly" || lower == "hourly" || lower == "every hour" {
		next, err := NextCronTime("0 * * * *", now)
		return ScheduleTypeCron, next, "0 * * * *", err
	}

	// Check standard 5-part cron syntax
	fields := strings.Fields(input)
	if len(fields) == 5 {
		next, err := NextCronTime(input, now)
		if err == nil {
			return ScheduleTypeCron, next, input, nil
		}
	}

	// Check relative duration (e.g. "in 15m", "10m", "2h", "1d", "30s")
	cleanDur := strings.TrimPrefix(lower, "in ")
	cleanDur = strings.TrimSpace(cleanDur)
	if dur, ok := parseCustomDuration(cleanDur); ok {
		if dur <= 0 {
			return "", time.Time{}, "", fmt.Errorf("duration must be positive")
		}
		return ScheduleTypeOnce, now.Add(dur), "", nil
	}

	// Check recurring interval like "every 30m", "every 2h", "every 1d"
	if strings.HasPrefix(lower, "every ") {
		intStr := strings.TrimSpace(strings.TrimPrefix(lower, "every "))
		if dur, ok := parseCustomDuration(intStr); ok {
			if dur < 10*time.Second {
				return "", time.Time{}, "", fmt.Errorf("interval must be at least 10 seconds")
			}
			return ScheduleTypeInterval, now.Add(dur), intStr, nil
		}
	}

	// Check absolute time formats
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"15:04:05",
		"15:04",
		"3:04pm",
		"3:04PM",
		"3pm",
		"3PM",
	}

	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, input, now.Location()); err == nil {
			// If layout did not include date, attach to today or tomorrow
			if !strings.Contains(layout, "2006") {
				target := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
				if !target.After(now) {
					target = target.AddDate(0, 0, 1) // Next day
				}
				return ScheduleTypeOnce, target, "", nil
			}
			return ScheduleTypeOnce, t, "", nil
		}
	}

	return "", time.Time{}, "", fmt.Errorf("could not parse schedule expression %q", input)
}

func parseCustomDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Support days ("1d", "2d", "3days", etc.)
	if strings.HasSuffix(s, "d") || strings.HasSuffix(s, "day") || strings.HasSuffix(s, "days") {
		numStr := strings.TrimRight(s, "days ")
		if days, err := strconv.ParseFloat(numStr, 64); err == nil && days > 0 {
			return time.Duration(days * 24 * float64(time.Hour)), true
		}
	}
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, true
	}
	return 0, false
}

// NextCronTime calculates the next run time after 'after' according to standard 5-part cron syntax.
// Cron format: [minute 0-59] [hour 0-23] [day 1-31] [month 1-12] [day-of-week 0-6 or 0-7, 0=Sun]
func NextCronTime(cronExpr string, after time.Time) (time.Time, error) {
	fields := strings.Fields(strings.TrimSpace(cronExpr))
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("invalid cron expression: expected 5 fields, got %d", len(fields))
	}

	minMatch, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid minute field: %w", err)
	}
	hourMatch, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid hour field: %w", err)
	}
	dayMatch, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day-of-month field: %w", err)
	}
	monthMatch, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid month field: %w", err)
	}
	dowMatch, err := parseCronField(fields[4], 0, 7)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day-of-week field: %w", err)
	}

	// Truncate to the next whole minute
	curr := after.Truncate(time.Minute).Add(time.Minute)

	// Search up to 5 years (approx 2,628,000 minutes)
	maxIterations := 60 * 24 * 365 * 5
	for i := 0; i < maxIterations; i++ {
		month := int(curr.Month())
		if !monthMatch(month) {
			// Skip to next month
			curr = time.Date(curr.Year(), curr.Month()+1, 1, 0, 0, 0, 0, curr.Location())
			continue
		}

		day := curr.Day()
		dow := int(curr.Weekday()) // 0 = Sunday
		dayMatches := dayMatch(day)
		dowMatches := dowMatch(dow) || (dow == 0 && dowMatch(7))

		// If day-of-month is wildcard, match on dow; if dow is wildcard, match on day; if both specified, either matches
		domWildcard := fields[2] == "*"
		dowWildcard := fields[4] == "*"
		dayFits := false
		if domWildcard && dowWildcard {
			dayFits = true
		} else if domWildcard {
			dayFits = dowMatches
		} else if dowWildcard {
			dayFits = dayMatches
		} else {
			dayFits = dayMatches || dowMatches
		}

		if !dayFits {
			// Skip to next day
			curr = time.Date(curr.Year(), curr.Month(), curr.Day()+1, 0, 0, 0, 0, curr.Location())
			continue
		}

		hour := curr.Hour()
		if !hourMatch(hour) {
			// Skip to next hour
			curr = time.Date(curr.Year(), curr.Month(), curr.Day(), curr.Hour()+1, 0, 0, 0, curr.Location())
			continue
		}

		minute := curr.Minute()
		if minMatch(minute) {
			return curr, nil
		}

		curr = curr.Add(time.Minute)
	}

	return time.Time{}, fmt.Errorf("could not find next execution time for cron %q within 5 years", cronExpr)
}

func parseCronField(expr string, minVal, maxVal int) (func(int) bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "*" {
		return func(v int) bool { return true }, nil
	}

	parts := strings.Split(expr, ",")
	matches := make(map[int]bool)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "*/") {
			stepStr := strings.TrimPrefix(part, "*/")
			step, err := strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step in %q", part)
			}
			for i := minVal; i <= maxVal; i += step {
				matches[i] = true
			}
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 != nil || err2 != nil || start > end || start < minVal || end > maxVal {
				return nil, fmt.Errorf("invalid range values in %q", part)
			}
			for i := start; i <= end; i++ {
				matches[i] = true
			}
			continue
		}

		val, err := strconv.Atoi(part)
		if err != nil || val < minVal || val > maxVal {
			return nil, fmt.Errorf("value %q out of range [%d-%d]", part, minVal, maxVal)
		}
		matches[val] = true
	}

	return func(v int) bool {
		return matches[v]
	}, nil
}
