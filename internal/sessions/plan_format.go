package sessions

import (
	"fmt"
	"strings"
)

// FormatPlanChecklist renders a compact Markdown checklist suitable for Telegram.
func FormatPlanChecklist(summary string, steps []string, completed int) string {
	cleanSteps := cleanPlanSteps(steps)
	if completed < 0 {
		completed = 0
	}
	if completed > len(cleanSteps) {
		completed = len(cleanSteps)
	}

	var sb strings.Builder
	sb.WriteString("📋 *Execution Plan*\n\n")
	if strings.TrimSpace(summary) != "" {
		sb.WriteString(strings.TrimSpace(summary))
		sb.WriteString("\n\n")
	}
	for i, step := range cleanSteps {
		marker := "○"
		switch {
		case i < completed:
			marker = "✓"
		case i == completed && completed < len(cleanSteps):
			marker = "●"
		}
		fmt.Fprintf(&sb, "%s %s\n", marker, step)
	}
	return strings.TrimSpace(sb.String())
}

func FormatPlanProgressShort(plan SessionPlan) string {
	cleanSteps := cleanPlanSteps(plan.Steps)
	total := len(cleanSteps)
	if total == 0 {
		return "Plan progress: no steps."
	}
	completed := plan.Completed
	if completed < 0 {
		completed = 0
	}
	if completed > total {
		completed = total
	}
	if plan.Status == PlanStatusCompleted {
		return fmt.Sprintf("Plan completed: %d/%d steps done.", total, total)
	}
	if plan.Status == PlanStatusDeferred {
		current := completed + 1
		if current > total {
			current = total
		}
		step := cleanSteps[current-1]
		if plan.DeferredUntil != nil {
			return fmt.Sprintf("Plan paused until %s. Step %d/%d pending: %s", plan.DeferredUntil.Format("15:04"), current, total, step)
		}
		return fmt.Sprintf("Plan paused. Step %d/%d pending: %s", current, total, step)
	}
	current := completed + 1
	if current > total {
		current = total
	}
	return fmt.Sprintf("Plan: step %d/%d - %s", current, total, cleanSteps[current-1])
}
