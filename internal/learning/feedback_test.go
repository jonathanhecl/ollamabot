package learning

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadClearFeedback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "feedback-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// LoadFeedback on non-existent file should return empty slice
	entries, err := LoadFeedback(tmpDir)
	if err != nil {
		t.Fatalf("LoadFeedback on non-existent file should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}

	// Save a few entries
	e1 := FeedbackEntry{Text: "Always respond in Spanish", Category: "preference", CreatedAt: time.Now()}
	e2 := FeedbackEntry{Text: "Don't use bullet points for short answers", Category: "correction", CreatedAt: time.Now()}

	if err := SaveFeedback(tmpDir, e1); err != nil {
		t.Fatalf("SaveFeedback e1 failed: %v", err)
	}
	if err := SaveFeedback(tmpDir, e2); err != nil {
		t.Fatalf("SaveFeedback e2 failed: %v", err)
	}

	// Load and verify
	entries, err = LoadFeedback(tmpDir)
	if err != nil {
		t.Fatalf("LoadFeedback failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Text != e1.Text {
		t.Errorf("entry 0 text mismatch: got %q, want %q", entries[0].Text, e1.Text)
	}
	if entries[1].Text != e2.Text {
		t.Errorf("entry 1 text mismatch: got %q, want %q", entries[1].Text, e2.Text)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(tmpDir, "feedback.json")); err != nil {
		t.Errorf("feedback.json not created: %v", err)
	}

	// Clear and verify
	if err := ClearFeedback(tmpDir); err != nil {
		t.Fatalf("ClearFeedback failed: %v", err)
	}
	entries, err = LoadFeedback(tmpDir)
	if err != nil {
		t.Fatalf("LoadFeedback after clear failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", len(entries))
	}
}

func TestSaveFeedbackAutoSetsCreatedAt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "feedback-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	entry := FeedbackEntry{Text: "Great job", Category: "praise"}
	if err := SaveFeedback(tmpDir, entry); err != nil {
		t.Fatalf("SaveFeedback failed: %v", err)
	}

	entries, _ := LoadFeedback(tmpDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be auto-set")
	}
}
