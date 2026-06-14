package tools

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteCommand_AllowedCommands(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Test a command that is NOT allowed
	_, err := executeCommand(ctx, tmpDir, "unauthorized_cmd", nil)
	if err == nil {
		t.Fatal("expected error for blocked command 'unauthorized_cmd'")
	}
	if !strings.Contains(err.Error(), "is not in the allowed list") {
		t.Errorf("expected allowed list error, got: %v", err)
	}

	// Test one of our new developer tools (e.g., go)
	// It should pass the allowed check (even if it fails to execute due to not being in PATH)
	_, err = executeCommand(ctx, tmpDir, "go", []string{"version"})
	if err != nil && strings.Contains(err.Error(), "is not in the allowed list") {
		t.Errorf("expected 'go' to be in the allowed list, got error: %v", err)
	}

	// Test git
	_, err = executeCommand(ctx, tmpDir, "git", []string{"--version"})
	if err != nil && strings.Contains(err.Error(), "is not in the allowed list") {
		t.Errorf("expected 'git' to be in the allowed list, got error: %v", err)
	}
}
