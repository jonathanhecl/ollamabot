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
