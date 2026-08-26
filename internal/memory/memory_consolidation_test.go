package memory

import (
	"os"
	"testing"
	"time"
)

func TestConsolidateAndPrune(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ollamabot_memory_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewStore(tmpDir)

	// Add entries: two identical texts, one near duplicate, one empty, and one unique
	now := time.Now()
	_ = store.Add(&Entry{
		ID:        "1",
		Text:      "User prefers writing backend code in Go with standard library",
		Category:  "tech_stack",
		Tags:      []string{"golang", "backend"},
		CreatedAt: now.Add(-10 * time.Minute),
	})
	_ = store.Add(&Entry{
		ID:        "2",
		Text:      "User prefers writing backend code in Go with standard library",
		Category:  "preferences",
		Tags:      []string{"go", "std"},
		CreatedAt: now,
	})
	_ = store.Add(&Entry{
		ID:        "3",
		Text:      "User prefers writing backend services in Go with standard library",
		Tags:      []string{"microservices"},
		CreatedAt: now.Add(-5 * time.Minute),
	})
	_ = store.Add(&Entry{
		ID:        "4",
		Text:      "   ",
		CreatedAt: now,
	})
	_ = store.Add(&Entry{
		ID:        "5",
		Text:      "PostgreSQL is deployed on port 5432 for analytical queries",
		Category:  "database",
		Tags:      []string{"postgres", "sql"},
		CreatedAt: now,
	})

	report, err := store.ConsolidateAndPrune(0.82)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.MergedCount < 1 {
		t.Errorf("expected at least 1 merged duplicate, got: %d", report.MergedCount)
	}
	if report.PrunedCount < 1 {
		t.Errorf("expected at least 1 pruned empty entry, got: %d", report.PrunedCount)
	}

	// Verify persistence
	store2 := NewStore(tmpDir)
	if store2.Count() != report.RemainingCount {
		t.Errorf("expected %d persisted entries, got %d", report.RemainingCount, store2.Count())
	}
}
