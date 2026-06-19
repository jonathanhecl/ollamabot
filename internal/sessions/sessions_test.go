package sessions

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveGetDeleteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sess := Session{
		ID:    GenerateID(),
		Title: "Test Session",
		Model: "test-model",
	}
	msg := map[string]any{
		"role":    "user",
		"content": "hello world",
	}
	raw, _ := json.Marshal(msg)
	sess.Messages = []json.RawMessage{raw}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// List should return the session
	list, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
	if list[0].ID != sess.ID {
		t.Fatalf("wrong session ID: %q", list[0].ID)
	}
	if list[0].Title != "Test Session" {
		t.Fatalf("wrong title: %q", list[0].Title)
	}
	// List should NOT include messages
	if list[0].Messages != nil {
		t.Fatalf("List should not include messages")
	}

	// Get should restore full session with messages
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}
	var restored map[string]any
	if err := json.Unmarshal(got.Messages[0], &restored); err != nil {
		t.Fatalf("Unmarshal message failed: %v", err)
	}
	if restored["content"] != "hello world" {
		t.Fatalf("wrong content: %v", restored["content"])
	}

	// Delete
	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	list, _ = store.List()
	if len(list) != 0 {
		t.Fatalf("expected 0 sessions after delete, got %d", len(list))
	}
}

func TestAttachmentExtractionAndRestore(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Create a message with a base64 image
	imageData := []byte("fake image binary content for testing")
	b64 := base64.StdEncoding.EncodeToString(imageData)
	msg := map[string]any{
		"role":    "user",
		"content": "check this image",
		"images":  []string{b64},
	}
	raw, _ := json.Marshal(msg)

	sess := Session{
		ID:       GenerateID(),
		Title:    "Attachment Test",
		Model:    "test-model",
		Messages: []json.RawMessage{raw},
	}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify the attachment file was extracted to disk
	attDir := store.attachmentsDir(sess.ID)
	entries, err := os.ReadDir(attDir)
	if err != nil {
		t.Fatalf("ReadDir attachments failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 attachment file, got %d", len(entries))
	}

	// Verify extracted JSON matches original
	diskData, err := os.ReadFile(filepath.Join(attDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile attachment failed: %v", err)
	}
	var storage attachmentStorage
	if err := json.Unmarshal(diskData, &storage); err != nil {
		t.Fatalf("Unmarshal attachment JSON failed: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(storage.Data)
	if err != nil {
		t.Fatalf("Decode base64 failed: %v", err)
	}
	if string(decoded) != string(imageData) {
		t.Fatalf("attachment content mismatch: got %q, expected %q", string(decoded), string(imageData))
	}

	// Get should restore the base64 images
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}

	var restored RawMsg
	if err := json.Unmarshal(got.Messages[0], &restored); err != nil {
		t.Fatalf("Unmarshal restored message: %v", err)
	}
	if len(restored.Images) != 1 {
		t.Fatalf("expected 1 restored image, got %d", len(restored.Images))
	}
	if restored.Images[0] != b64 {
		t.Fatalf("restored base64 does not match original")
	}
}

func TestAudioAttachmentRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	audioData := []byte("fake wav audio bytes for testing")
	b64 := base64.StdEncoding.EncodeToString(audioData)
	dataURL := "data:audio/wav;base64," + b64

	msg := map[string]any{
		"role":    "user",
		"content": "",
		"attachments": []map[string]any{
			{
				"name":          "mic_record.wav",
				"mime":          "audio/wav",
				"kind":          "audio",
				"data":          b64,
				"url":           dataURL,
				"transcription": "hello world transcription",
				"unreadable":    false,
			},
		},
	}
	raw, _ := json.Marshal(msg)

	sess := Session{
		ID:       GenerateID(),
		Title:    "Audio Test",
		Model:    "test-model",
		Messages: []json.RawMessage{raw},
	}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}

	var restored RawMsg
	if err := json.Unmarshal(got.Messages[0], &restored); err != nil {
		t.Fatalf("Unmarshal restored message: %v", err)
	}
	if len(restored.Attachments) != 1 {
		t.Fatalf("expected 1 restored attachment, got %d", len(restored.Attachments))
	}
	att := restored.Attachments[0]
	if att.Kind != "audio" {
		t.Errorf("expected kind=audio, got %q", att.Kind)
	}
	if att.Data != b64 {
		t.Errorf("audio base64 data mismatch after roundtrip")
	}
	if att.Transcription != "hello world transcription" {
		t.Errorf("transcription mismatch: got %q", att.Transcription)
	}
	if att.Mime != "audio/wav" {
		t.Errorf("mime mismatch: got %q", att.Mime)
	}
}

func TestAssistantMetricsPreserved(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	metrics := map[string]any{
		"total_duration":       int64(5_000_000_000),
		"eval_count":           int64(200),
		"eval_duration":        int64(4_000_000_000),
		"prompt_eval_count":    int64(50),
		"prompt_eval_duration": int64(500_000_000),
		"load_duration":        int64(100_000_000),
	}
	assistantMsg := map[string]any{
		"role":      "assistant",
		"content":   "I'm ready when you are!",
		"timestamp": "2024-01-01T12:00:00Z",
		"metrics":   metrics,
	}
	raw, _ := json.Marshal(assistantMsg)

	sess := Session{
		ID:       GenerateID(),
		Title:    "Metrics Test",
		Model:    "test-model",
		Messages: []json.RawMessage{raw},
	}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	var restored map[string]any
	if err := json.Unmarshal(got.Messages[0], &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	restoredMetrics, ok := restored["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics not preserved after save/load, got: %T %v", restored["metrics"], restored["metrics"])
	}
	if restoredMetrics["total_duration"] == nil {
		t.Errorf("total_duration missing from restored metrics")
	}
}

func TestGenerateIDUniqueness(t *testing.T) {
	const n = 1000
	ids := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := GenerateID()
		if _, exists := ids[id]; exists {
			t.Fatalf("duplicate ID generated at iteration %d: %q", i, id)
		}
		ids[id] = struct{}{}
	}
}

func TestGetNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, err := store.Get("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestEmptySessions(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sess := Session{
		ID:    GenerateID(),
		Title: "Empty Session",
		Model: "test-model",
	}

	// Saving an empty session should NOT write to disk
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save empty session failed: %v", err)
	}

	// Directory should not exist on disk
	sessDir := store.sessionDir(sess.ID)
	if _, err := os.Stat(sessDir); err == nil {
		t.Fatalf("expected empty session folder to not exist on disk")
	}

	// It should still be retrievable from the store while in-memory
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("expected empty session to be retrievable from in-memory map: %v", err)
	}
	if got.ID != sess.ID {
		t.Fatalf("retrieved empty session ID mismatch")
	}

	// List should NOT return empty sessions since they are not on disk
	list, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected List to return 0 sessions, got %d", len(list))
	}

	// After adding a message, it should no longer be empty and should be written to disk
	msg := map[string]any{"role": "user", "content": "hi"}
	raw, _ := json.Marshal(msg)
	sess.Messages = []json.RawMessage{raw}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save non-empty session failed: %v", err)
	}

	// Directory should exist now
	if _, err := os.Stat(sessDir); err != nil {
		t.Fatalf("expected non-empty session folder to exist on disk: %v", err)
	}

	// List should now return the session
	list2, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list2) != 1 {
		t.Fatalf("expected List to return 1 session, got %d", len(list2))
	}
}

func TestListOrdersByLastMessageAt(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	saveSession := func(title, timestamp string) Session {
		t.Helper()
		sess := Session{
			ID:    GenerateID(),
			Title: title,
			Model: "test-model",
		}
		raw, err := json.Marshal(map[string]any{
			"role":      "user",
			"content":   title,
			"timestamp": timestamp,
		})
		if err != nil {
			t.Fatal(err)
		}
		sess.Messages = []json.RawMessage{raw}
		if err := store.Save(sess); err != nil {
			t.Fatalf("Save(%q) failed: %v", title, err)
		}
		return sess
	}

	older := saveSession("Older chat", "2026-01-01T10:00:00Z")
	time.Sleep(5 * time.Millisecond)
	newer := saveSession("Newer chat", "2026-06-01T10:00:00Z")

	list, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
	if list[0].ID != newer.ID {
		t.Fatalf("expected newest message session first, got %q before %q", list[0].Title, list[1].Title)
	}
	if list[0].LastMessageAt.IsZero() {
		t.Fatalf("expected last_message_at on listed session")
	}
	_ = older
}

func TestListCacheUpdatesSingleEntry(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	raw, err := json.Marshal(map[string]any{
		"role":      "user",
		"content":   "first",
		"timestamp": "2026-01-01T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := Session{ID: GenerateID(), Title: "Chat", Model: "test", Messages: []json.RawMessage{raw}}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	list1, err := store.List()
	if err != nil || len(list1) != 1 {
		t.Fatalf("expected 1 cached session, got %v err=%v", list1, err)
	}

	raw2, _ := json.Marshal(map[string]any{
		"role":      "user",
		"content":   "second",
		"timestamp": "2026-06-01T12:00:00Z",
	})
	sess.Messages = append(sess.Messages, raw2)
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	entry, err := store.GetListEntry(sess.ID)
	if err != nil {
		t.Fatalf("GetListEntry failed: %v", err)
	}
	if !entry.LastMessageAt.Equal(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected last_message_at: %v", entry.LastMessageAt)
	}

	list2, err := store.List()
	if err != nil || len(list2) != 1 {
		t.Fatalf("expected 1 cached session after update, got %v", list2)
	}
	if list2[0].LastMessageAt != entry.LastMessageAt {
		t.Fatalf("list cache out of sync with entry")
	}
}
