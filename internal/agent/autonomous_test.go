package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestAutonomousManager_GenerateInitialTodos(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
		expectedTasks []string
		wantErr       bool
	}{
		{
			name:          "valid JSON schema matching todos object",
			modelResponse: `{"todos": [{"id": "task-1", "content": "Setup HTML foundation"}, {"id": "task-2", "content": "Style components"}]}`,
			expectedTasks: []string{"Setup HTML foundation", "Style components"},
			wantErr:       false,
		},
		{
			name:          "fallback legacy JSON array",
			modelResponse: `[{"id": "task-1", "content": "Setup HTML foundation"}, {"id": "task-2", "content": "Style components"}]`,
			expectedTasks: []string{"Setup HTML foundation", "Style components"},
			wantErr:       false,
		},
		{
			name:          "invalid response text",
			modelResponse: "Invalid response content",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				resp := ollama.ChatResponse{
					Message: ollama.Message{
						Role:    "assistant",
						Content: tt.modelResponse,
					},
					Done: true,
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			cfg := config.Config{
				OllamaDefaultModel: "test-model",
			}
			client := ollama.NewClient(server.URL)
			am := NewAutonomousManager(config.NewManager(cfg), client, nil)

			todos, err := am.generateInitialTodos(context.Background(), "test-project", "some goal")
			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error: %v, got: %v", tt.wantErr, err)
			}
			if !tt.wantErr {
				if len(todos) != len(tt.expectedTasks) {
					t.Fatalf("expected %d todos, got %d", len(tt.expectedTasks), len(todos))
				}
				for i, task := range todos {
					if task.Content != tt.expectedTasks[i] {
						t.Errorf("todo %d expected content %q, got %q", i, tt.expectedTasks[i], task.Content)
					}
					if task.Status != "pending" {
						t.Errorf("todo %d expected pending status, got %q", i, task.Status)
					}
				}
			}
		})
	}
}

// helperSaveProject writes a project.json directly to a workspace dir.
func helperSaveProject(t *testing.T, workspace, id string, proj Project) {
	t.Helper()
	dir := filepath.Join(workspace, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	data, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.json"), data, 0o644); err != nil {
		t.Fatalf("write project.json: %v", err)
	}
}

func TestAutonomousManager_RecoverStaleTasks(t *testing.T) {
	workspace := t.TempDir()

	cfg := config.Config{
		Workspace:                  workspace,
		AutonomousStaleTaskMinutes: 5,
		OllamaDefaultModel:         "test-model",
	}
	am := NewAutonomousManager(config.NewManager(cfg), ollama.NewClient("http://localhost:11434"), nil)

	// Project A: stale in_progress task (updated 1 hour ago) -> should be reset.
	staleTime := time.Now().Add(-1 * time.Hour)
	projA := Project{
		ID:          "proj-a",
		Name:        "Project A",
		Goal:        "g",
		Status:      "in_progress",
		CurrentTask: "old task",
		Todos: []ProjectTodo{
			{ID: "t1", Content: "stale task", Status: "in_progress", UpdatedAt: staleTime},
			{ID: "t2", Content: "fresh pending", Status: "pending", UpdatedAt: time.Now()},
		},
		CreatedAt: staleTime,
		UpdatedAt: staleTime,
	}
	helperSaveProject(t, workspace, "proj-a", projA)

	// Project B: fresh in_progress task (updated 1 minute ago) -> should stay.
	freshTime := time.Now().Add(-1 * time.Minute)
	projB := Project{
		ID:          "proj-b",
		Name:        "Project B",
		Goal:        "g",
		Status:      "in_progress",
		CurrentTask: "running task",
		Todos: []ProjectTodo{
			{ID: "t1", Content: "running task", Status: "in_progress", UpdatedAt: freshTime},
		},
		CreatedAt: freshTime,
		UpdatedAt: freshTime,
	}
	helperSaveProject(t, workspace, "proj-b", projB)

	// Project C: completed project -> should be untouched.
	projC := Project{
		ID:     "proj-c",
		Name:   "Project C",
		Goal:   "g",
		Status: "completed",
		Todos: []ProjectTodo{
			{ID: "t1", Content: "done", Status: "completed", UpdatedAt: staleTime},
		},
		CreatedAt: staleTime,
		UpdatedAt: staleTime,
	}
	helperSaveProject(t, workspace, "proj-c", projC)

	am.RecoverStaleTasks()

	// Verify Project A: stale task reset to pending, project back to pending.
	reloadedA, err := am.LoadProject("proj-a")
	if err != nil {
		t.Fatalf("LoadProject proj-a: %v", err)
	}
	if reloadedA.Todos[0].Status != "pending" {
		t.Errorf("proj-a t1: expected pending, got %q", reloadedA.Todos[0].Status)
	}
	if reloadedA.Todos[1].Status != "pending" {
		t.Errorf("proj-a t2: expected pending, got %q", reloadedA.Todos[1].Status)
	}
	if reloadedA.Status != "pending" {
		t.Errorf("proj-a: expected project status pending, got %q", reloadedA.Status)
	}
	if reloadedA.CurrentTask != "" {
		t.Errorf("proj-a: expected CurrentTask cleared, got %q", reloadedA.CurrentTask)
	}

	// Verify Project B: still in_progress.
	reloadedB, err := am.LoadProject("proj-b")
	if err != nil {
		t.Fatalf("LoadProject proj-b: %v", err)
	}
	if reloadedB.Todos[0].Status != "in_progress" {
		t.Errorf("proj-b t1: expected in_progress, got %q", reloadedB.Todos[0].Status)
	}
	if reloadedB.Status != "in_progress" {
		t.Errorf("proj-b: expected project status in_progress, got %q", reloadedB.Status)
	}

	// Verify Project C: untouched.
	reloadedC, err := am.LoadProject("proj-c")
	if err != nil {
		t.Fatalf("LoadProject proj-c: %v", err)
	}
	if reloadedC.Status != "completed" {
		t.Errorf("proj-c: expected completed, got %q", reloadedC.Status)
	}
}

func TestAutonomousManager_RecoverStaleTasks_DefaultThreshold(t *testing.T) {
	workspace := t.TempDir()
	// AutonomousStaleTaskMinutes=0 -> should default to 30 minutes.
	cfg := config.Config{
		Workspace:                  workspace,
		AutonomousStaleTaskMinutes: 0,
		OllamaDefaultModel:         "test-model",
	}
	am := NewAutonomousManager(config.NewManager(cfg), ollama.NewClient("http://localhost:11434"), nil)

	// Task updated 10 minutes ago: under default 30-min threshold -> stays in_progress.
	freshTime := time.Now().Add(-10 * time.Minute)
	proj := Project{
		ID:          "proj-x",
		Name:        "Project X",
		Goal:        "g",
		Status:      "in_progress",
		CurrentTask: "running",
		Todos: []ProjectTodo{
			{ID: "t1", Content: "running", Status: "in_progress", UpdatedAt: freshTime},
		},
		CreatedAt: freshTime,
		UpdatedAt: freshTime,
	}
	helperSaveProject(t, workspace, "proj-x", proj)

	am.RecoverStaleTasks()

	reloaded, err := am.LoadProject("proj-x")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if reloaded.Todos[0].Status != "in_progress" {
		t.Errorf("expected in_progress (under default 30min threshold), got %q", reloaded.Todos[0].Status)
	}
}

func TestAutonomousManager_SetOnTaskCompletion(t *testing.T) {
	cfg := config.Config{OllamaDefaultModel: "test-model"}
	am := NewAutonomousManager(config.NewManager(cfg), ollama.NewClient("http://localhost:11434"), nil)

	called := false
	am.SetOnTaskCompletion(func(proj Project, task ProjectTodo, err error) {
		called = true
	})

	am.notifyTaskCompletion(Project{}, ProjectTodo{}, nil)
	if !called {
		t.Errorf("expected notifyTaskCompletion to invoke registered callback")
	}

	// Unset and verify it does not panic when no callback is registered.
	am.SetOnTaskCompletion(nil)
	am.notifyTaskCompletion(Project{}, ProjectTodo{}, nil)
}

func TestVerificationResponse_Parsing(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantOK   bool
		wantSuc  bool
		wantGaps int
	}{
		{
			name:     "success with no gaps",
			raw:      `{"success": true, "evidence": "files exist", "gaps": []}`,
			wantOK:   true,
			wantSuc:  true,
			wantGaps: 0,
		},
		{
			name:     "failure with gaps",
			raw:      `{"success": false, "evidence": "missing app.js", "gaps": ["app.js not created", "index.html empty"]}`,
			wantOK:   true,
			wantSuc:  false,
			wantGaps: 2,
		},
		{
			name:     "wrapped in markdown fences",
			raw:      "```json\n{\"success\": true, \"evidence\": \"ok\"}\n```",
			wantOK:   true,
			wantSuc:  true,
			wantGaps: 0,
		},
		{
			name:     "invalid JSON",
			raw:      "not json at all",
			wantOK:   false,
			wantSuc:  false,
			wantGaps: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := trimFencesForTest(tt.raw)
			var vr verificationResponse
			err := json.Unmarshal([]byte(raw), &vr)
			if (err == nil) != tt.wantOK {
				t.Fatalf("expected parse ok=%v, got err=%v", tt.wantOK, err)
			}
			if tt.wantOK {
				if vr.Success != tt.wantSuc {
					t.Errorf("expected success=%v, got %v", tt.wantSuc, vr.Success)
				}
				if len(vr.Gaps) != tt.wantGaps {
					t.Errorf("expected %d gaps, got %d", tt.wantGaps, len(vr.Gaps))
				}
			}
		})
	}
}

// trimFencesForTest mirrors the markdown fence stripping done in verifyTask.
func trimFencesForTest(raw string) string {
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	return strings.TrimSpace(raw)
}

func TestAutonomousStepCollector(t *testing.T) {
	c := newAutonomousStepCollector()
	c.OnToolStart("write_file", map[string]any{"file_path": "index.html"}, "internal")
	time.Sleep(2 * time.Millisecond)
	c.OnToolResult("write_file", "ok", "internal")
	c.OnToolStart("execute_command", map[string]any{"command": "go"}, "internal")
	c.OnToolResult("execute_command", "Error: command failed", "internal")
	c.OnEvent("plan_mode", map[string]any{"mode": "smart"})

	steps := c.snapshot()
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0].Name != "write_file" || steps[0].Status != "done" {
		t.Fatalf("unexpected step 0: %#v", steps[0])
	}
	if steps[0].DurationMs <= 0 {
		t.Fatalf("expected positive duration for step 0, got %d", steps[0].DurationMs)
	}
	if steps[1].Status != "error" {
		t.Fatalf("expected error status for step 1, got %#v", steps[1])
	}
	if steps[2].Type != "system_event" || steps[2].Name != "plan_mode" {
		t.Fatalf("unexpected step 2: %#v", steps[2])
	}
}

func TestWriteAutonomousStep(t *testing.T) {
	var sb strings.Builder
	writeAutonomousStep(&sb, 1, sessions.Step{
		Type:       "tool_exec",
		Name:       "read_file",
		Arguments:  map[string]any{"path": "a.txt"},
		Result:     "hello",
		Status:     "done",
		DurationMs: 12,
	})
	out := sb.String()
	for _, want := range []string{"`read_file`", "done", "12ms", "hello", "a.txt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in structured step output, got:\n%s", want, out)
		}
	}

	var eventSB strings.Builder
	writeAutonomousStep(&eventSB, 1, sessions.Step{
		Type:    "system_event",
		Name:    "plan_mode",
		Content: "Plan confirmation mode: smart",
	})
	if !strings.Contains(eventSB.String(), "Plan confirmation mode: smart") {
		t.Fatalf("expected event content, got:\n%s", eventSB.String())
	}
}
