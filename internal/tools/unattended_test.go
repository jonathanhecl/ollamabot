package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func unattendedToolCall(name string, args map[string]any) ollama.ToolCall {
	raw, _ := json.Marshal(args)
	return ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      name,
			Arguments: raw,
		},
	}
}

// An unattended run (scheduled task, background loop, plan resume) has nobody
// to answer. ask_clarification would fail and present_plan would be approved by
// the run itself, leaving an active plan no one reviews; the plan monitor then
// retries it forever. Neither tool may reach the model.
func TestUnattendedRunsHideInteractiveTools(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetChannel("scheduler")
	registry.SetUnattended(true)

	for _, name := range []string{"ask_clarification", "present_plan"} {
		if hasTool(registry, name) {
			t.Fatalf("%s should not be offered in an unattended run", name)
		}
	}

	// A stale definition must not be able to block the turn either.
	_, err := registry.Execute(context.Background(), unattendedToolCall("present_plan", map[string]any{
		"summary": "Configure the task",
		"steps":   []string{"Ask the user for the hour"},
	}))
	if err == nil || !strings.Contains(err.Error(), "unattended run") {
		t.Fatalf("expected present_plan to be rejected in an unattended run, got %v", err)
	}
	_, err = registry.Execute(context.Background(), unattendedToolCall("ask_clarification", map[string]any{
		"question": "Which hour?",
	}))
	if err == nil || !strings.Contains(err.Error(), "unattended run") {
		t.Fatalf("expected ask_clarification to be rejected in an unattended run, got %v", err)
	}

	// Tools that a run really needs stay available.
	for _, name := range []string{"todo_write", "read_file", "complete_plan_step"} {
		if !hasTool(registry, name) {
			t.Fatalf("%s should stay available in an unattended run", name)
		}
	}
}

// An interactive turn keeps the whole set: the same registry is rebuilt per
// turn, and only unattended runs lose the tools.
func TestInteractiveRunsKeepInteractiveTools(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetChannel("web")

	for _, name := range []string{"ask_clarification", "present_plan"} {
		if !hasTool(registry, name) {
			t.Fatalf("%s should be offered in an interactive turn", name)
		}
	}
}
