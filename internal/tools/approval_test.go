package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type mockApprovalHandler struct {
	lastTool string
	lastArgs map[string]any
	response bool
	called   bool
}

func (m *mockApprovalHandler) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	m.called = true
	m.lastTool = toolName
	m.lastArgs = args
	return m.response, nil
}

func TestApprovalHandlerRiskyTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ollamabot_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// We create a directory named "workspace" inside the temp directory to test safe path bypass,
	// and also keep the temp root as the "outside" path.
	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}

	registry := NewRegistry(false, tmpDir, nil, nil, "")
	handler := &mockApprovalHandler{response: true}
	registry.SetApprovalHandler(handler)

	// 1. Risky tool (Write) to a file OUTSIDE a folder named "workspace"
	// Since r.workspace is set to tmpDir, writing to "test.txt" will resolve to tmpDir/test.txt,
	// which does NOT contain "workspace" as a segment in its absolute path.
	args := map[string]any{
		"file_path": "test.txt",
		"contents":  "hello approval",
	}
	argsBytes, _ := json.Marshal(args)
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "Write",
			Arguments: argsBytes,
		},
	}

	_, err = registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !handler.called {
		t.Error("expected ApprovalHandler to be called for file outside workspace segment")
	}
	if handler.lastTool != "Write" {
		t.Errorf("expected tool to be 'Write', got %q", handler.lastTool)
	}

	// Clean up written file
	_ = os.Remove(filepath.Join(tmpDir, "test.txt"))

	// 2. Test DENIAL
	handler.called = false
	handler.response = false

	res, err := registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res != "Error: Execution denied by user." {
		t.Errorf("expected denied message, got %q", res)
	}
	if !handler.called {
		t.Error("expected ApprovalHandler to be called")
	}

	// 3. Test Bypassing approval for path INSIDE a folder named "workspace"
	// Write to "workspace/test.txt", which resolves to tmpDir/workspace/test.txt.
	// This absolute path contains the segment "workspace", so it should bypass approval!
	handler.called = false
	handler.response = true

	safeArgs := map[string]any{
		"file_path": "workspace/test.txt",
		"contents":  "hello safe",
	}
	safeArgsBytes, _ := json.Marshal(safeArgs)
	safeCall := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "Write",
			Arguments: safeArgsBytes,
		},
	}

	_, err = registry.Execute(context.Background(), safeCall)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if handler.called {
		t.Error("expected ApprovalHandler to be BYPASSED for file inside 'workspace' folder segment")
	}

	// Check that file was actually written
	writtenContent, err := os.ReadFile(filepath.Join(workspaceDir, "test.txt"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(writtenContent) != "hello safe" {
		t.Errorf("expected content 'hello safe', got %q", string(writtenContent))
	}
}
