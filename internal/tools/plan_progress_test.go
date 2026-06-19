package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestCompletePlanStepTool(t *testing.T) {
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
	var notified sessions.SessionPlan
	registry.SetPlanProgressHandler(func(sessionID string, plan sessions.SessionPlan) {
		if sessionID != sess.ID {
			t.Fatalf("callback sessionID = %q, want %q", sessionID, sess.ID)
		}
		notified = plan
	})

	argsBytes, _ := json.Marshal(map[string]any{"note": "first step is done"})
	call := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      "complete_plan_step",
			Arguments: argsBytes,
		},
	}
	result, err := registry.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "Step 1 completed. Next: Second Note: first step is done" {
		t.Fatalf("unexpected result: %q", result)
	}
	if notified.Completed != 1 {
		t.Fatalf("expected callback with completed=1, got %#v", notified)
	}
	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Completed != 1 {
		t.Fatalf("active plan not updated: %#v", loaded.ActivePlan)
	}
}
