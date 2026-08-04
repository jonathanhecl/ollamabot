package agent

import (
	"strings"
	"testing"
)

func TestLoopRecoveryWarningDetection(t *testing.T) {
	tests := []struct {
		name                 string
		result               string
		isDeniedOrTimeout    bool
		isMCPValidationError bool
	}{
		{
			name:                 "User denial",
			result:               "Error: tool approval failed: Denied by user.",
			isDeniedOrTimeout:    true,
			isMCPValidationError: false,
		},
		{
			name:                 "Approval timeout",
			result:               "Error: tool approval failed: approval timeout",
			isDeniedOrTimeout:    true,
			isMCPValidationError: false,
		},
		{
			name:                 "MCP schema 32602 error",
			result:               "Error: tool returned error: MCP error -32602: Input validation error",
			isDeniedOrTimeout:    false,
			isMCPValidationError: true,
		},
		{
			name:                 "Normal error",
			result:               "Error: file not found",
			isDeniedOrTimeout:    false,
			isMCPValidationError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			denied := strings.Contains(tc.result, "Denied by user") || strings.Contains(tc.result, "approval timeout") || strings.Contains(tc.result, "approval failed")
			mcpErr := strings.Contains(tc.result, "MCP error -32602") || strings.Contains(tc.result, "Input validation error")

			if denied != tc.isDeniedOrTimeout {
				t.Errorf("isDeniedOrTimeout got %v, expected %v", denied, tc.isDeniedOrTimeout)
			}
			if mcpErr != tc.isMCPValidationError {
				t.Errorf("isMCPValidationError got %v, expected %v", mcpErr, tc.isMCPValidationError)
			}
		})
	}
}
