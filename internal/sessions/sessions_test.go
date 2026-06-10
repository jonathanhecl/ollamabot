package sessions

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
