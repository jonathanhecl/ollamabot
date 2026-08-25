package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestSafetyPolicyReadOnly(t *testing.T) {
	ws := t.TempDir()
	testFile := filepath.Join(ws, "hello.txt")
	_ = os.WriteFile(testFile, []byte("hello world"), 0644)

	reg := NewRegistry(false, ws, nil, nil, "", SearchConfig{})
	reg.SetSafetyPolicy(SafetyPolicyReadOnly)

	// 1. Read tool should succeed under ReadOnly policy
	readArgs, _ := json.Marshal(map[string]any{"path": "hello.txt"})
	out, err := reg.Execute(context.Background(), ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "read_file",
			Arguments: readArgs,
		},
	})
	if err != nil {
		t.Fatalf("expected read_file to succeed in ReadOnly mode, got: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("expected read_file output, got: %s", out)
	}

	// 2. Write tool should be blocked under ReadOnly policy
	writeArgs, _ := json.Marshal(map[string]any{"file_path": "test.txt", "contents": "data"})
	_, err = reg.Execute(context.Background(), ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "write_file",
			Arguments: writeArgs,
		},
	})
	if err == nil {
		t.Fatalf("expected write_file to be blocked by ReadOnly policy")
	}
	if !strings.Contains(err.Error(), "blocked by ReadOnly safety policy") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestDryRunSimulation(t *testing.T) {
	ws := t.TempDir()
	existingFile := filepath.Join(ws, "sample.txt")
	_ = os.WriteFile(existingFile, []byte("hello old world"), 0644)

	reg := NewRegistry(false, ws, nil, nil, "", SearchConfig{})
	reg.SetDryRun(true)

	// 1. Simulate write_file
	writeArgs, _ := json.Marshal(map[string]any{
		"file_path": "new_file.txt",
		"contents":  "simulated content",
	})
	out, err := reg.Execute(context.Background(), ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "write_file",
			Arguments: writeArgs,
		},
	})
	if err != nil {
		t.Fatalf("expected dry-run write_file to succeed, got: %v", err)
	}
	if !strings.Contains(out, "[DRY-RUN SIMULATION]") {
		t.Fatalf("expected dry-run simulation prefix, got: %s", out)
	}
	// Verify file was NOT created on disk
	if _, err := os.Stat(filepath.Join(ws, "new_file.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected new_file.txt to NOT exist on disk during dry-run")
	}

	// 2. Simulate edit_file
	editArgs, _ := json.Marshal(map[string]any{
		"file_path":   "sample.txt",
		"target":      "old",
		"replacement": "new",
	})
	out, err = reg.Execute(context.Background(), ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "edit_file",
			Arguments: editArgs,
		},
	})
	if err != nil {
		t.Fatalf("expected dry-run edit_file to succeed, got: %v", err)
	}
	if !strings.Contains(out, "[DRY-RUN SIMULATION]") {
		t.Fatalf("expected dry-run simulation prefix, got: %s", out)
	}
	// Verify content on disk remains unchanged
	raw, _ := os.ReadFile(existingFile)
	if string(raw) != "hello old world" {
		t.Fatalf("expected sample.txt on disk to be unmodified, got: %s", string(raw))
	}

	// 3. Simulate execute_command
	cmdArgs, _ := json.Marshal(map[string]any{
		"command": "git",
		"args":    []any{"status"},
	})
	out, err = reg.Execute(context.Background(), ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "execute_command",
			Arguments: cmdArgs,
		},
	})
	if err != nil {
		t.Fatalf("expected dry-run execute_command to succeed, got: %v", err)
	}
	if !strings.Contains(out, "[DRY-RUN SIMULATION]") {
		t.Fatalf("expected dry-run simulation prefix, got: %s", out)
	}
}

func TestSensitiveFilesProtection(t *testing.T) {
	ws := t.TempDir()

	sensitiveFiles := []string{
		".env",
		".env.local",
		"id_rsa",
		"id_ed25519",
		"server.key",
		"cert.pem",
		".git/config",
		".ssh/id_rsa",
	}

	for _, sf := range sensitiveFiles {
		_, err := ResolveAndValidatePath(ws, sf)
		if err == nil {
			t.Errorf("expected sensitive file %q to be rejected, but it was allowed", sf)
		}
	}
}
