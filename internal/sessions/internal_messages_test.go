package sessions

import "testing"

func TestIsInternalTimelineMessage(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"There is an approved plan with remaining steps (currently step 4 of 5: test).", true},
		{"Plan monitor resume: active plan appears stalled. Continue the approved plan from step 4 of 5: test.", true},
		{"There are still pending TODO items. Continue executing the remaining steps with tool calls.", true},
		{"Previous attempt returned only thinking. Please produce a visible text response or call a tool.", true},
		{"This is a summary of the optimized previous context:\nfoo", false},
		{"The current session contains the following attachments", true},
		{"Hello, how can I help?", false},
		{"🎯 *Goal Started:* build something", false},
	}
	for _, tt := range tests {
		if got := IsInternalTimelineMessage(tt.content); got != tt.want {
			t.Errorf("IsInternalTimelineMessage(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}
