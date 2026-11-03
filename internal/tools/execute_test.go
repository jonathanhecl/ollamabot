package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/mcp"
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

func TestRegistry_SetMCPManager_Inactive(t *testing.T) {
	reg := NewRegistry(false, t.TempDir(), nil, nil, "", SearchConfig{})
	m := mcp.NewManager("nonexistent.json")
	reg.SetMCPManager(m)

	for _, d := range reg.Definitions() {
		if d.Function.Name == "mcp_list_servers" {
			t.Fatal("expected mcp_list_servers to NOT be registered when MCP has no active servers")
		}
	}
}
