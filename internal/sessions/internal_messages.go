package sessions

import "strings"

// IsInternalTimelineMessage reports whether a system message is agent-internal
// and must not appear in the user-facing session timeline.
func IsInternalTimelineMessage(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	return isInjectedContextMessage(content) ||
		strings.Contains(content, "There is an approved plan with remaining steps") ||
		strings.Contains(content, "There are still pending TODO items") ||
		strings.Contains(content, "Plan monitor resume:") ||
		strings.Contains(content, "Previous attempt returned only thinking") ||
		strings.Contains(content, "This is a summary of the optimized previous context:") ||
		strings.HasPrefix(content, "Error: Malformed JSON arguments in <invoke") ||
		strings.HasPrefix(content, "Error: Invalid JSON syntax in <invoke") ||
		strings.HasPrefix(content, "Error: Missing closing tag </invoke>") ||
		strings.HasPrefix(content, "Error: Malformed JSON arguments in <tool_call") ||
		strings.HasPrefix(content, "Error: Invalid JSON syntax in <tool_call") ||
		strings.HasPrefix(content, "Error: Missing closing tag </tool_call>")
}
