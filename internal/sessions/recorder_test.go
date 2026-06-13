package sessions

import (
	"encoding/json"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestAppendThinkingStep(t *testing.T) {
	steps := AppendThinkingStep(nil, "first ")
	steps = AppendThinkingStep(steps, "second")

	if len(steps) != 1 {
		t.Fatalf("expected one thinking step, got %d", len(steps))
	}
	if steps[0].Type != "thinking" || steps[0].Content != "first second" {
		t.Fatalf("unexpected thinking step: %#v", steps[0])
	}

	steps = append(steps, Step{Type: "tool_exec", Name: "search"})
	steps = AppendThinkingStep(steps, "after tool")
	if len(steps) != 3 {
		t.Fatalf("expected new thinking step after tool, got %d", len(steps))
	}
}

func TestFinalizeStepsWithThinkingDeduplicatesLegacyThinking(t *testing.T) {
	steps := []Step{
		{Type: "thinking", Content: "streamed", Status: "running"},
		{Type: "tool_exec", Name: "search", Status: "done"},
	}

	final := FinalizeStepsWithThinking(steps, "legacy")
	if len(final) != 2 {
		t.Fatalf("expected no duplicated thinking step, got %d", len(final))
	}
	if final[0].Content != "streamed" {
		t.Fatalf("expected streamed thinking to win, got %q", final[0].Content)
	}
	if final[0].Status != "" || final[1].Status != "" {
		t.Fatalf("expected ephemeral status stripped: %#v", final)
	}

	final = FinalizeStepsWithThinking([]Step{{Type: "tool_exec", Name: "search"}}, "legacy")
	if len(final) != 2 || final[0].Type != "thinking" || final[0].Content != "legacy" {
		t.Fatalf("expected legacy thinking prepended, got %#v", final)
	}
}

func TestRecorderSnapshotsMultipleAssistantTurns(t *testing.T) {
	rec := NewRecorder(nil, "", []RawMsg{{Role: "user", Content: "hi", Timestamp: "t0"}}, "model", "web")

	rec.OnThinking("thinking 1")
	rec.OnContent("first")
	rec.OnDone(ollama.ChatResponse{TotalDuration: 10, EvalCount: 1})
	rec.OnContent("second")

	rec.mu.Lock()
	messages := rec.snapshotMessagesLocked()
	rec.mu.Unlock()

	if len(messages) != 3 {
		t.Fatalf("expected base user + two assistants, got %d", len(messages))
	}

	var first RawMsg
	if err := json.Unmarshal(messages[1], &first); err != nil {
		t.Fatal(err)
	}
	if first.Content != "first" || first.Model != "model" || first.Channel != "web" {
		t.Fatalf("unexpected first assistant: %#v", first)
	}
	if len(first.Steps) != 1 || first.Steps[0].Type != "thinking" {
		t.Fatalf("expected thinking step on first assistant: %#v", first.Steps)
	}
	if first.Metrics == nil || first.Metrics.TotalDuration != 10 {
		t.Fatalf("expected metrics on first assistant: %#v", first.Metrics)
	}

	var second RawMsg
	if err := json.Unmarshal(messages[2], &second); err != nil {
		t.Fatal(err)
	}
	if second.Content != "second" {
		t.Fatalf("unexpected second assistant content: %q", second.Content)
	}
}

func TestRecorderSaveGenerationRejectsStaleSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := Session{ID: "s1", Title: "session", Messages: mustRawMessages(t, []RawMsg{{Role: "user", Content: "base"}})}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder(store, "s1", []RawMsg{{Role: "user", Content: "base"}}, "model", "telegram")
	rec.mu.Lock()
	stale := mustRawMessages(t, []RawMsg{{Role: "user", Content: "stale"}})
	rec.saveGen++
	rec.mu.Unlock()

	rec.saveSnapshot(0, stale)

	got, err := store.Get("s1")
	if err != nil {
		t.Fatal(err)
	}
	var msg RawMsg
	if err := json.Unmarshal(got.Messages[0], &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Content != "base" {
		t.Fatalf("stale snapshot overwrote session: %#v", msg)
	}
}

func mustRawMessages(t *testing.T, messages []RawMsg) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	return out
}

func TestMergeFinalHistoryPreservesBaseAssistantSteps(t *testing.T) {
	baseHist := []RawMsg{
		{
			Role:      "user",
			Content:   "hi",
			Timestamp: "t0",
		},
		{
			Role:      "assistant",
			Content:   "hello",
			Timestamp: "t1",
			Steps: []Step{
				{Type: "thinking", Content: "think 1"},
				{Type: "image_progress", ImageURL: "http://example.com/img.png", Status: "done"},
			},
			Metrics: &Metrics{TotalDuration: 42},
		},
		{
			Role:      "user",
			Content:   "next prompt",
			Timestamp: "t2",
		},
	}

	rec := NewRecorder(nil, "", baseHist, "model", "web")

	// Simulate streaming events of the new turn
	rec.OnThinking("think 2")
	rec.OnContent("response 2")
	rec.OnDone(ollama.ChatResponse{TotalDuration: 100})

	// The full conversation history as received from Ollama API
	finalHist := []ollama.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "next prompt"},
		{Role: "assistant", Content: "response 2"},
	}

	rec.mu.Lock()
	mergedRaw := rec.mergeFinalHistoryLocked(finalHist)
	rec.mu.Unlock()

	if len(mergedRaw) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(mergedRaw))
	}

	var firstUser RawMsg
	json.Unmarshal(mergedRaw[0], &firstUser)
	if firstUser.Timestamp != "t0" || firstUser.Content != "hi" {
		t.Errorf("first user message mismatch: %+v", firstUser)
	}

	var firstAssistant RawMsg
	json.Unmarshal(mergedRaw[1], &firstAssistant)
	if firstAssistant.Timestamp != "t1" || firstAssistant.Content != "hello" {
		t.Errorf("first assistant message mismatch: %+v", firstAssistant)
	}
	if len(firstAssistant.Steps) != 2 || firstAssistant.Steps[1].ImageURL != "http://example.com/img.png" {
		t.Errorf("expected first assistant steps to be preserved, got %+v", firstAssistant.Steps)
	}
	if firstAssistant.Metrics == nil || firstAssistant.Metrics.TotalDuration != 42 {
		t.Errorf("expected first assistant metrics to be preserved, got %+v", firstAssistant.Metrics)
	}

	var secondUser RawMsg
	json.Unmarshal(mergedRaw[2], &secondUser)
	if secondUser.Timestamp != "t2" || secondUser.Content != "next prompt" {
		t.Errorf("second user message mismatch: %+v", secondUser)
	}

	var secondAssistant RawMsg
	json.Unmarshal(mergedRaw[3], &secondAssistant)
	if secondAssistant.Content != "response 2" {
		t.Errorf("second assistant content mismatch: %q", secondAssistant.Content)
	}
	if len(secondAssistant.Steps) != 1 || secondAssistant.Steps[0].Content != "think 2" {
		t.Errorf("expected second assistant steps, got %+v", secondAssistant.Steps)
	}
	if secondAssistant.Metrics == nil || secondAssistant.Metrics.TotalDuration != 100 {
		t.Errorf("expected second assistant metrics, got %+v", secondAssistant.Metrics)
	}
}
