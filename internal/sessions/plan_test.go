package sessions

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionPlanLifecycle(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := Session{
		ID:    GenerateID(),
		Title: "Plan session",
		Model: "test-model",
	}
	raw, _ := json.Marshal(RawMsg{Role: "user", Content: "do work"})
	sess.Messages = []json.RawMessage{raw}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	plan, err := ActivatePlan(store, sess.ID, "Do the work", []string{"First", "", "Second"})
	if err != nil {
		t.Fatalf("ActivatePlan failed: %v", err)
	}
	if plan.Status != PlanStatusActive || plan.Completed != 0 || len(plan.Steps) != 2 {
		t.Fatalf("unexpected active plan: %#v", plan)
	}

	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Steps[0] != "First" {
		t.Fatalf("active plan was not persisted: %#v", loaded.ActivePlan)
	}

	plan, msg, err := CompletePlanStep(store, sess.ID, "done")
	if err != nil {
		t.Fatalf("CompletePlanStep failed: %v", err)
	}
	if plan.Completed != 1 || plan.Status != PlanStatusActive {
		t.Fatalf("unexpected first completion: %#v", plan)
	}
	if !strings.Contains(msg, "Next: Second") {
		t.Fatalf("unexpected completion message: %q", msg)
	}

	plan, msg, err = CompletePlanStep(store, sess.ID, "")
	if err != nil {
		t.Fatalf("CompletePlanStep final failed: %v", err)
	}
	if plan.Completed != 2 || plan.Status != PlanStatusCompleted {
		t.Fatalf("unexpected final completion: %#v", plan)
	}
	if msg != "All plan steps completed." {
		t.Fatalf("unexpected final message: %q", msg)
	}
}

func TestClearActivePlan(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := Session{ID: GenerateID(), Title: "Plan session", Model: "test-model"}
	raw, _ := json.Marshal(RawMsg{Role: "user", Content: "do work"})
	sess.Messages = []json.RawMessage{raw}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := ActivatePlan(store, sess.ID, "Do", []string{"One"}); err != nil {
		t.Fatalf("ActivatePlan failed: %v", err)
	}
	if err := ClearActivePlan(store, sess.ID); err != nil {
		t.Fatalf("ClearActivePlan failed: %v", err)
	}
	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if loaded.ActivePlan != nil {
		t.Fatalf("expected nil active plan, got %#v", loaded.ActivePlan)
	}
}

func TestFormatPlanChecklist(t *testing.T) {
	got := FormatPlanChecklist("Summary", []string{"One", "Two", "Three"}, 1)
	for _, want := range []string{
		"📋 *Execution Plan*",
		"Summary",
		"✓ One",
		"● Two",
		"○ Three",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("FormatPlanChecklist missing %q in:\n%s", want, got)
		}
	}
}

func TestNormalizePlanSteps(t *testing.T) {
	got := NormalizePlanSteps([]string{"1. First step 2. Second step 3. Third step"})
	if len(got) != 3 {
		t.Fatalf("expected 3 steps, got %d: %#v", len(got), got)
	}
	if got[0] != "First step" || got[1] != "Second step" || got[2] != "Third step" {
		t.Fatalf("unexpected normalized steps: %#v", got)
	}

	unchanged := NormalizePlanSteps([]string{"Alpha", "Beta"})
	if len(unchanged) != 2 || unchanged[0] != "Alpha" || unchanged[1] != "Beta" {
		t.Fatalf("expected unchanged steps, got %#v", unchanged)
	}
}

func TestActivatePlanNormalizesNumberedSteps(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := Session{ID: GenerateID(), Title: "Plan session", Model: "test-model"}
	raw, _ := json.Marshal(RawMsg{Role: "user", Content: "do work"})
	sess.Messages = []json.RawMessage{raw}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	plan, err := ActivatePlan(store, sess.ID, "Summary", []string{"1. One 2. Two 3. Three"})
	if err != nil {
		t.Fatalf("ActivatePlan failed: %v", err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %#v", plan.Steps)
	}
}
