package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type mockPlanConfirmationHandler struct {
	lastSummary string
	lastSteps   []string
	response    bool
	called      bool
}

func (m *mockPlanConfirmationHandler) RequestPlanApproval(ctx context.Context, summary string, steps []string) (bool, error) {
	m.called = true
	m.lastSummary = summary
	m.lastSteps = steps
	return m.response, nil
}

func TestPlanConfirmationHandler(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	handler := &mockPlanConfirmationHandler{response: true}
	registry.SetPlanConfirmationHandler(handler)

	// Test 1: Successful plan approval (approved)
	args := map[string]any{
		"summary": "This is a plan to modify config.",
		"steps":   []string{"Step 1: Edit config.go", "Step 2: Edit tools.go"},
	}
	argsBytes, _ := json.Marshal(args)
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "present_plan",
			Arguments: argsBytes,
		},
	}

	res, err := registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !handler.called {
		t.Error("expected PlanConfirmationHandler to be called")
	}
	if handler.lastSummary != "This is a plan to modify config." {
		t.Errorf("expected summary 'This is a plan to modify config.', got %q", handler.lastSummary)
	}
	if len(handler.lastSteps) != 2 || handler.lastSteps[0] != "Step 1: Edit config.go" || handler.lastSteps[1] != "Step 2: Edit tools.go" {
		t.Errorf("unexpected steps: %v", handler.lastSteps)
	}
	if res != "Plan approved by the user. Proceed with the steps." {
		t.Errorf("expected plan approved response, got %q", res)
	}

	// Test 2: Plan rejected
	handler.called = false
	handler.response = false
	res, err = registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !handler.called {
		t.Error("expected PlanConfirmationHandler to be called")
	}
	if res != "Plan rejected by the user. Please stop and ask the user for clarification or propose a new plan." {
		t.Errorf("expected plan rejected response, got %q", res)
	}

	// Test 3: Validation failure (empty steps)
	handler.called = false
	invalidArgs := map[string]any{
		"summary": "This is a plan to modify config.",
		"steps":   []string{},
	}
	invalidArgsBytes, _ := json.Marshal(invalidArgs)
	invalidCall := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "present_plan",
			Arguments: invalidArgsBytes,
		},
	}

	_, err = registry.Execute(context.Background(), invalidCall)
	if err == nil {
		t.Fatal("expected execute to fail with empty steps")
	}
	if handler.called {
		t.Error("expected PlanConfirmationHandler NOT to be called for invalid arguments")
	}
}
