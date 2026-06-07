package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type mockClarificationHandler struct {
	lastQuestion string
	lastOptions  []string
	response     string
	called       bool
}

func (m *mockClarificationHandler) RequestClarification(ctx context.Context, question string, options []string) (string, error) {
	m.called = true
	m.lastQuestion = question
	m.lastOptions = options
	return m.response, nil
}

func TestClarificationHandler(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	handler := &mockClarificationHandler{response: "Option B"}
	registry.SetClarificationHandler(handler)

	// Test 1: Successful clarification request
	args := map[string]any{
		"question": "Which branch should I merge?",
		"options":  []string{"Option A", "Option B"},
	}
	argsBytes, _ := json.Marshal(args)
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "ask_clarification",
			Arguments: argsBytes,
		},
	}

	res, err := registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !handler.called {
		t.Error("expected ClarificationHandler to be called")
	}
	if handler.lastQuestion != "Which branch should I merge?" {
		t.Errorf("expected question 'Which branch should I merge?', got %q", handler.lastQuestion)
	}
	if len(handler.lastOptions) != 2 || handler.lastOptions[0] != "Option A" || handler.lastOptions[1] != "Option B" {
		t.Errorf("unexpected options: %v", handler.lastOptions)
	}
	if res != "Option B" {
		t.Errorf("expected response 'Option B', got %q", res)
	}

	// Test 2: Validation failure (less than 2 options)
	handler.called = false
	invalidArgs := map[string]any{
		"question": "Which branch?",
		"options":  []string{"Option A"},
	}
	invalidArgsBytes, _ := json.Marshal(invalidArgs)
	invalidCall := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "ask_clarification",
			Arguments: invalidArgsBytes,
		},
	}

	_, err = registry.Execute(context.Background(), invalidCall)
	if err == nil {
		t.Fatal("expected execute to fail with less than 2 options")
	}
	if handler.called {
		t.Error("expected ClarificationHandler NOT to be called for invalid arguments")
	}
}
