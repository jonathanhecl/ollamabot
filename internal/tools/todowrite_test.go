package tools

import (
	"testing"
)

func TestTodoStore(t *testing.T) {
	store := NewTodoStore()

	// Initial empty checklist
	if len(store.Snapshot()) != 0 {
		t.Errorf("expected empty store, got %d items", len(store.Snapshot()))
	}

	initial := []TodoItem{
		{ID: "task-1", Content: "Plan the changes", Status: "in_progress"},
		{ID: "task-2", Content: "Write the code", Status: "pending"},
	}

	// 1. Initial replace
	finalList := store.Apply(false, initial)
	if len(finalList) != 2 {
		t.Fatalf("expected 2 items, got %d", len(finalList))
	}
	if finalList[0].ID != "task-1" || finalList[0].Status != TodoStatusInProgress {
		t.Errorf("unexpected first item: %+v", finalList[0])
	}

	// 2. Merge changes
	incoming := []TodoItem{
		{ID: "task-1", Status: "completed"}, // flip status, omit content
		{ID: "task-3", Content: "Run tests", Status: "pending"},
	}

	mergedList := store.Apply(true, incoming)
	if len(mergedList) != 3 {
		t.Fatalf("expected 3 items, got %d", len(mergedList))
	}

	// Verify task-1 status updated while preserving content
	if mergedList[0].ID != "task-1" || mergedList[0].Content != "Plan the changes" || mergedList[0].Status != TodoStatusCompleted {
		t.Errorf("unexpected merged item task-1: %+v", mergedList[0])
	}

	// Verify task-3 was appended
	if mergedList[2].ID != "task-3" || mergedList[2].Content != "Run tests" || mergedList[2].Status != TodoStatusPending {
		t.Errorf("unexpected merged item task-3: %+v", mergedList[2])
	}
}

func TestValidateTodoContent(t *testing.T) {
	incoming := []TodoItem{
		{ID: "1", Content: "   ", Status: "pending"}, // whitespace only
	}
	if err := ValidateTodoContent(incoming, false, nil); err == nil {
		t.Error("expected validation error for empty content, got nil")
	}

	incoming[0].Content = "12345" // numeric placeholder only
	if err := ValidateTodoContent(incoming, false, nil); err == nil {
		t.Error("expected validation error for numeric placeholder, got nil")
	}

	incoming[0].Content = "Write some tests" // valid
	if err := ValidateTodoContent(incoming, false, nil); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestDecodeTodos(t *testing.T) {
	raw := []any{
		map[string]any{"id": "step-1", "content": "Decode this", "status": "pending"},
	}

	decoded, err := DecodeTodos(raw)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(decoded) != 1 || decoded[0].ID != "step-1" || decoded[0].Content != "Decode this" {
		t.Errorf("unexpected decoded list: %+v", decoded)
	}
}

func TestRenderTodosForModel(t *testing.T) {
	items := []TodoItem{
		{ID: "step-1", Content: "Write code", Status: "completed"},
	}
	rendered := RenderTodosForModel(items)
	expected := "TODOs (1):\n- [x] step-1: Write code"
	if rendered != expected {
		t.Errorf("expected %q, got %q", expected, rendered)
	}
}
