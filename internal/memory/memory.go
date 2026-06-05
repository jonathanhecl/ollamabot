package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry is a single memory chunk with its embedding vector.
type Entry struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Embedding []float64 `json:"embedding"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store keeps memory entries in memory and persists them as JSONL.
type Store struct {
	mu       sync.RWMutex
	entries  []Entry
	path     string
	dirty    bool
}

// NewStore creates or loads a memory store from the given directory.
// It uses memory.jsonl inside that directory.
func NewStore(dir string) *Store {
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "memory.jsonl")
	s := &Store{path: path}
	_ = s.load()
	return s
}

// Add inserts a new entry and persists the store.
func (s *Store) Add(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	s.entries = append(s.entries, e)
	s.dirty = true
	return s.flush()
}

// Search computes cosine similarity between the query embedding and all stored
// embeddings, returning the top k results sorted by score descending.
func (s *Store) Search(queryEmbedding []float64, k int) []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 || len(queryEmbedding) == 0 {
		return nil
	}

	results := make([]Result, 0, len(s.entries))
	for _, e := range s.entries {
		if len(e.Embedding) == 0 {
			continue
		}
		score := cosineSimilarity(queryEmbedding, e.Embedding)
		results = append(results, Result{
			Entry: e,
			Score: score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if k > 0 && k < len(results) {
		results = results[:k]
	}
	return results
}

// List returns all entries ordered by created_at descending.
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Delete removes an entry by ID and persists.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := make([]Entry, 0, len(s.entries))
	found := false
	for _, e := range s.entries {
		if e.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, e)
	}
	if !found {
		return fmt.Errorf("entry not found")
	}
	s.entries = filtered
	s.dirty = true
	return s.flush()
}

// Count returns the number of stored entries.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Result pairs an entry with its similarity score.
type Result struct {
	Entry
	Score float64 `json:"score"`
}

func (s *Store) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		s.entries = append(s.entries, e)
	}
	return scanner.Err()
}

func (s *Store) flush() error {
	if !s.dirty {
		return nil
	}
	f, err := os.Create(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, e := range s.entries {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	s.dirty = false
	return w.Flush()
}

func cosineSimilarity(a, b []float64) float64 {
	var dot, normA, normB float64
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		av := a[i]
		bv := b[i]
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
