package memory

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddAndSearch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Add entries with known embeddings (using similarity < 0.85 to avoid deduplication)
	e1 := Entry{Text: "Go programming language", Embedding: []float64{1, 0, 0}}
	e2 := Entry{Text: "Rust programming language", Embedding: []float64{0.5, 0.866, 0}}
	e3 := Entry{Text: "French cuisine recipes", Embedding: []float64{0, 0, 1}}

	if err := store.Add(e1); err != nil {
		t.Fatalf("Add e1: %v", err)
	}
	if err := store.Add(e2); err != nil {
		t.Fatalf("Add e2: %v", err)
	}
	if err := store.Add(e3); err != nil {
		t.Fatalf("Add e3: %v", err)
	}

	if store.Count() != 3 {
		t.Fatalf("expected 3 entries, got %d", store.Count())
	}

	// Search for something similar to "programming" → should find e1 and e2 first
	results := store.Search([]float64{1, 0, 0}, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Text != "Go programming language" {
		t.Fatalf("expected 'Go programming language' as top result, got %q", results[0].Text)
	}
	if results[1].Text != "Rust programming language" {
		t.Fatalf("expected 'Rust programming language' as second result, got %q", results[1].Text)
	}
	// First result should be a perfect match (cosine = 1.0)
	if math.Abs(results[0].Score-1.0) > 1e-9 {
		t.Fatalf("expected score ~1.0, got %f", results[0].Score)
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	e := Entry{Text: "to be deleted", Embedding: []float64{1, 0}}
	if err := store.Add(e); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", store.Count())
	}

	entries := store.List()
	id := entries[0].ID

	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected 0 entries after delete, got %d", store.Count())
	}

	// Deleting again should fail
	if err := store.Delete(id); err == nil {
		t.Fatal("expected error for double delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Delete("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestListOrder(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	now := time.Now()
	e1 := Entry{Text: "first", Embedding: []float64{1, 0}, CreatedAt: now.Add(-time.Second)}
	e2 := Entry{Text: "second", Embedding: []float64{0, 1}, CreatedAt: now}

	_ = store.Add(e1)
	_ = store.Add(e2)

	entries := store.List()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// List returns newest first
	if entries[0].Text != "second" {
		t.Fatalf("expected 'second' first, got %q", entries[0].Text)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	e := Entry{Text: "persistent data", Embedding: []float64{1, 2, 3}}
	if err := store.Add(e); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify JSONL file exists
	jsonlPath := filepath.Join(dir, "memory.jsonl")
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatalf("JSONL file not found: %v", err)
	}

	// Create a new store from the same directory — it should load persisted data
	store2 := NewStore(dir)
	if store2.Count() != 1 {
		t.Fatalf("expected 1 entry after reload, got %d", store2.Count())
	}
	entries := store2.List()
	if entries[0].Text != "persistent data" {
		t.Fatalf("expected 'persistent data', got %q", entries[0].Text)
	}
}

func TestCosineSimilarityOrthogonal(t *testing.T) {
	// Orthogonal vectors should have 0 similarity
	score := cosineSimilarity([]float64{1, 0, 0}, []float64{0, 1, 0})
	if math.Abs(score) > 1e-9 {
		t.Fatalf("expected ~0 for orthogonal vectors, got %f", score)
	}
}

func TestCosineSimilarityIdentical(t *testing.T) {
	score := cosineSimilarity([]float64{3, 4}, []float64{3, 4})
	if math.Abs(score-1.0) > 1e-9 {
		t.Fatalf("expected ~1.0 for identical vectors, got %f", score)
	}
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	score := cosineSimilarity([]float64{0, 0}, []float64{1, 1})
	if score != 0 {
		t.Fatalf("expected 0 for zero vector, got %f", score)
	}
}

func TestSearchEmptyStore(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	results := store.Search([]float64{1, 0}, 5)
	if results != nil {
		t.Fatalf("expected nil results for empty store, got %v", results)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Add(Entry{Text: "test", Embedding: []float64{1}})

	results := store.Search([]float64{}, 5)
	if results != nil {
		t.Fatalf("expected nil results for empty query, got %v", results)
	}
}

func TestUpdateEmbeddings(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	e1 := Entry{Text: "first entry", Embedding: []float64{1, 2}}
	e2 := Entry{Text: "second entry", Embedding: []float64{3, 4}}

	_ = store.Add(e1)
	_ = store.Add(e2)

	entries := store.List()
	var id1, id2 string
	for _, entry := range entries {
		if entry.Text == "first entry" {
			id1 = entry.ID
		} else if entry.Text == "second entry" {
			id2 = entry.ID
		}
	}

	newEmbeddings := map[string][]float64{
		id1: {10, 20},
		id2: {30, 40},
	}

	if err := store.UpdateEmbeddings(newEmbeddings); err != nil {
		t.Fatalf("UpdateEmbeddings: %v", err)
	}

	// Load a new store to verify persistence
	store2 := NewStore(dir)
	entries2 := store2.List()
	for _, entry := range entries2 {
		if entry.Text == "first entry" {
			if entry.Embedding[0] != 10 || entry.Embedding[1] != 20 {
				t.Fatalf("expected [10, 20], got %v", entry.Embedding)
			}
		} else if entry.Text == "second entry" {
			if entry.Embedding[0] != 30 || entry.Embedding[1] != 40 {
				t.Fatalf("expected [30, 40], got %v", entry.Embedding)
			}
		}
	}
}

func TestDeduplication(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// 1. Test exact text duplicate
	e1 := Entry{Text: "  User likes apples  ", Embedding: []float64{1.0, 0.0}}
	e2 := Entry{Text: "user likes apples", Embedding: []float64{1.0, 0.0}}

	_ = store.Add(e1)
	if store.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", store.Count())
	}

	_ = store.Add(e2)
	if store.Count() != 1 {
		t.Fatalf("expected 1 entry after adding exact text duplicate, got %d", store.Count())
	}

	// Verify that the entry has the new values
	list := store.List()
	if list[0].Text != "user likes apples" {
		t.Fatalf("expected text to be 'user likes apples', got %q", list[0].Text)
	}

	// 2. Test near-duplicate by vector similarity (threshold >= 0.85)
	// cosine similarity of [1.0, 0.0] and [0.90, 0.435889894] is 0.90
	e3 := Entry{Text: "user enjoys apples", Embedding: []float64{0.90, 0.435889894}}
	_ = store.Add(e3)
	if store.Count() != 1 {
		t.Fatalf("expected 1 entry after near-duplicate addition, got %d", store.Count())
	}

	list = store.List()
	if list[0].Text != "user enjoys apples" {
		t.Fatalf("expected 'user enjoys apples' to overwrite previous entry, got %q", list[0].Text)
	}

	// 3. Test non-duplicate (below threshold)
	// cosine similarity of [0.90, 0.435889894] and [0.50, 0.8660254] is 0.45 + 0.377 = 0.827, which is < 0.85
	e4 := Entry{Text: "user eats oranges", Embedding: []float64{0.50, 0.8660254}}
	_ = store.Add(e4)
	if store.Count() != 2 {
		t.Fatalf("expected 2 entries after non-duplicate addition, got %d", store.Count())
	}
}
