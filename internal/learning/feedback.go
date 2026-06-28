package learning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FeedbackEntry is a user-submitted text feedback for the reflector.
type FeedbackEntry struct {
	Text      string    `json:"text"`
	Category  string    `json:"category"` // "correction", "preference", "praise"
	CreatedAt time.Time `json:"created_at"`
}

var feedbackMu sync.Mutex

// feedbackPath returns the full path to feedback.json inside the sessions directory.
func feedbackPath(sessionsPath string) string {
	return filepath.Join(sessionsPath, "feedback.json")
}

// SaveFeedback appends a feedback entry to the global feedback.json file.
func SaveFeedback(sessionsPath string, entry FeedbackEntry) error {
	feedbackMu.Lock()
	defer feedbackMu.Unlock()

	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	path := feedbackPath(sessionsPath)
	entries, err := LoadFeedback(sessionsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	entries = append(entries, entry)

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(sessionsPath, 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// LoadFeedback reads all feedback entries from feedback.json.
// Returns an empty slice if the file does not exist.
func LoadFeedback(sessionsPath string) ([]FeedbackEntry, error) {
	path := feedbackPath(sessionsPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []FeedbackEntry{}, nil
		}
		return nil, err
	}

	var entries []FeedbackEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}

	if entries == nil {
		entries = []FeedbackEntry{}
	}

	return entries, nil
}

// ClearFeedback removes all feedback entries by truncating the file.
func ClearFeedback(sessionsPath string) error {
	feedbackMu.Lock()
	defer feedbackMu.Unlock()

	path := feedbackPath(sessionsPath)
	if err := os.MkdirAll(sessionsPath, 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte("[]"), 0o644)
}
