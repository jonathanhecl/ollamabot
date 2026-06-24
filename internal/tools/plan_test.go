package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

type mockPlanConfirmationHandler struct {
	lastSummary string
	lastSteps   []string
	response    bool
	called      bool
}

func TestDeferPlanContinuation(t *testing.T) {
	store := sessions.NewStore(t.TempDir())
	sess := sessions.Session{
		ID:    sessions.GenerateID(),
		Title: "Plan session",
		Model: "test-model",
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := sessions.ActivatePlan(store, sess.ID, "Do work", []string{"First", "Second"}); err != nil {
		t.Fatalf("ActivatePlan failed: %v", err)
	}

	var progress sessions.SessionPlan
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSessionID(sess.ID)
	registry.SetSessionStore(store)
	registry.SetPlanProgressHandler(func(sessionID string, plan sessions.SessionPlan) {
		progress = plan
	})

	argsBytes, _ := json.Marshal(map[string]any{
		"reason":            "waiting for a long-running external check",
		"resume_after":      "30m",
		"follow_up_summary": "Continue with the second step.",
		"user_message":      "I paused this plan and will resume it later.",
	})
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "defer_plan_continuation",
			Arguments: argsBytes,
		},
	}

	res, err := registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res, "I paused this plan") {
		t.Fatalf("expected user message in result, got %q", res)
	}
	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Status != sessions.PlanStatusDeferred {
		t.Fatalf("expected deferred plan, got %#v", loaded.ActivePlan)
	}
	if loaded.ActivePlan.DeferredUntil == nil || !loaded.ActivePlan.DeferredUntil.After(time.Now()) {
		t.Fatalf("expected future defer time, got %#v", loaded.ActivePlan.DeferredUntil)
	}
	if progress.Status != sessions.PlanStatusDeferred {
		t.Fatalf("expected progress handler to receive deferred plan, got %#v", progress)
	}
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

func TestPresentPlanSkipsWhenActivePlanExists(t *testing.T) {
	store := sessions.NewStore(t.TempDir())
	sess := sessions.Session{
		ID:    sessions.GenerateID(),
		Title: "Plan session",
		Model: "test-model",
	}
	raw, _ := json.Marshal(sessions.RawMsg{Role: "user", Content: "do work"})
	sess.Messages = []json.RawMessage{raw}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := sessions.ActivatePlan(store, sess.ID, "Do work", []string{"First", "Second"}); err != nil {
		t.Fatalf("ActivatePlan failed: %v", err)
	}

	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSessionID(sess.ID)
	registry.SetSessionStore(store)
	handler := &mockPlanConfirmationHandler{response: true}
	registry.SetPlanConfirmationHandler(handler)

	argsBytes, _ := json.Marshal(map[string]any{
		"summary": "Another plan",
		"steps":   []string{"Step A", "Step B"},
	})
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
	if handler.called {
		t.Fatal("expected PlanConfirmationHandler NOT to be called when plan is already active")
	}
	if !strings.Contains(res, "Plan already approved") {
		t.Fatalf("expected guard response, got %q", res)
	}
}
