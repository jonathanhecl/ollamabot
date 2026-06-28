package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
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
	workspaceDir := t.TempDir()
	registry := NewRegistry(false, workspaceDir, nil, nil, "", SearchConfig{})
	handler := &mockApprovalHandler{response: true}
	registry.SetApprovalHandler(handler)

	args := map[string]any{
		"file_path": "test.txt",
		"contents":  "hello approval",
	}
	argsBytes, _ := json.Marshal(args)
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "write_file",
			Arguments: argsBytes,
		},
	}

	_, err := registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if handler.called {
		t.Error("expected ApprovalHandler to be bypassed for files inside the configured workspace")
	}
	writtenContent, err := os.ReadFile(filepath.Join(workspaceDir, "test.txt"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(writtenContent) != "hello approval" {
		t.Errorf("expected content 'hello approval', got %q", string(writtenContent))
	}

	handler.called = false
	handler.response = false

	execArgs := map[string]any{"command": "python3", "args": []any{"-V"}}
	execArgsBytes, _ := json.Marshal(execArgs)
	execCall := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "execute_command",
			Arguments: execArgsBytes,
		},
	}
	res, err := registry.Execute(context.Background(), execCall)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res != "Error: Execution denied by user." {
		t.Errorf("expected denied message, got %q", res)
	}
	if !handler.called {
		t.Error("expected ApprovalHandler to be called")
	}
	if handler.lastTool != "execute_command" {
		t.Errorf("expected tool to be 'execute_command', got %q", handler.lastTool)
	}
}

func TestApprovalSignatureNormalizesDuplicateCommand(t *testing.T) {
	workspace := t.TempDir()
	sigA, labelA := sessions.FormatApprovalSignature("execute_command", map[string]any{
		"command": "python3",
		"args":    []any{"test_extraction_script.py"},
	}, workspace)
	sigB, labelB := sessions.FormatApprovalSignature("execute_command", map[string]any{
		"command": "python3",
		"args":    []any{"python3", "test_extraction_script.py"},
	}, workspace)

	if sigA != sigB {
		t.Fatalf("expected duplicate command signature to normalize, got %q vs %q", sigA, sigB)
	}
	if labelA != labelB {
		t.Fatalf("expected duplicate command labels to normalize, got %q vs %q", labelA, labelB)
	}
}

func TestApprovalServiceSessionGrant(t *testing.T) {
	store := sessions.NewStore(t.TempDir())
	sess := sessions.Session{ID: "approval-session", Title: "Approval", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	cfg := config.Config{Workspace: t.TempDir()}
	service := sessions.NewApprovalService(store, config.NewManager(cfg))
	args := map[string]any{"command": "python3", "args": []any{"test_extraction_script.py"}}

	rememberSent := false
	var respondErr error
	service.RegisterNotifier(sess.ID, func(_ string, approval sessions.PendingApproval) {
		rememberSent = true
		respondErr = service.RespondApproval(approval.ID, sessions.ApprovalDecision{Approved: true, RememberForSession: true})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	approved, err := service.RequestApproval(ctx, sess.ID, "execute_command", args)
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if !approved {
		t.Fatal("expected approval")
	}
	if !rememberSent || respondErr != nil {
		t.Fatalf("notifier response failed: sent=%v err=%v", rememberSent, respondErr)
	}
	if !service.HasGrant(sess.ID, "execute_command", args) {
		loaded, _ := store.Get(sess.ID)
		t.Fatalf("expected session grant, pending=%#v grants=%#v", loaded.PendingApproval, loaded.ApprovalGrants)
	}
}

func TestAutonomousApprovalPolicySkipsSafeCommandApproval(t *testing.T) {
	workspaceDir := t.TempDir()
	registry := NewRegistry(false, workspaceDir, nil, nil, "", SearchConfig{})
	registry.SetApprovalPolicy(ApprovalPolicyAutonomous)
	handler := &mockApprovalHandler{response: false}
	registry.SetApprovalHandler(handler)

	args := map[string]any{"command": "go", "args": []any{"version"}}
	argsBytes, _ := json.Marshal(args)
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "execute_command",
			Arguments: argsBytes,
		},
	}

	if _, err := registry.Execute(context.Background(), call); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if handler.called {
		t.Fatal("expected safe autonomous command to bypass approval")
	}
}
