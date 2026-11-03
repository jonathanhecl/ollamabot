package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/mcp"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
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

func TestRegistry_SendTelegram(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry(false, tmpDir, nil, nil, "", SearchConfig{})
	mock := &mockTelegramSender{}
	reg.SetTelegramConfig("test-token", []string{"112233"})
	reg.SetCustomTelegramSender(mock)

	call := ollama.ToolCall{
		Function: ollama.ToolFunction{
			Name:      "send_telegram",
			Arguments: json.RawMessage(`{"message": "Hello from registry tool"}`),
		},
	}

	res, err := reg.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error executing send_telegram: %v", err)
	}
	if !strings.Contains(res, "112233") {
		t.Errorf("expected output to mention chat ID 112233, got: %s", res)
	}
	if len(mock.sentMessages) != 1 || mock.sentMessages[0] != "Hello from registry tool" {
		t.Errorf("expected sent message, got: %v", mock.sentMessages)
	}
}
