package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is a single memory chunk with optional embedding vector and metadata.
type Entry struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Embedding []float64 `json:"embedding,omitempty"`
	Source    string    `json:"source,omitempty"`
	Category  string    `json:"category,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store keeps memory entries in memory and persists them as JSONL.
type Store struct {
	mu      sync.RWMutex
	entries []Entry
	path    string
	dirty   bool
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

// Add inserts a new entry and persists the store. It automatically removes any existing entries
// that are highly similar (cosine similarity >= 0.85 or exact text match) to prevent duplication.
func (s *Store) Add(e *Entry) error {
	if e == nil {
		return fmt.Errorf("nil entry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Automatic threshold-based deduplication
	const similarityThreshold = 0.85
	var filtered []Entry
	var removedCount int

	for _, existing := range s.entries {
		isDuplicate := false
		normExisting := strings.TrimSpace(strings.ToLower(existing.Text))
		normNew := strings.TrimSpace(strings.ToLower(e.Text))

		if normExisting == normNew {
			isDuplicate = true
		} else if len(e.Embedding) > 0 && len(existing.Embedding) > 0 {
			score := cosineSimilarity(e.Embedding, existing.Embedding)
			if score >= similarityThreshold {
				isDuplicate = true
			}
		}

		if isDuplicate {
			removedCount++
		} else {
			filtered = append(filtered, existing)
		}
	}

	if removedCount > 0 {
		s.entries = filtered
		s.dirty = true
	}

	if e.ID == "" {
		e.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	s.entries = append(s.entries, *e)
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

// SearchText performs token-based lexical keyword matching on memory texts.
// Returns results with scores in [0.0, 1.0] sorted by relevance descending.
func (s *Store) SearchText(query string, k int) []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return nil
	}

	tokens := tokenize(query)
	phrase := strings.TrimSpace(strings.ToLower(query))
	if len(tokens) == 0 && len(phrase) == 0 {
		return nil
	}

	results := make([]Result, 0, len(s.entries))
	for _, e := range s.entries {
		score := computeTextMatchScore(tokens, phrase, e.Text)
		if score > 0.05 {
			results = append(results, Result{
				Entry: e,
				Score: score,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if k > 0 && k < len(results) {
		results = results[:k]
	}
	return results
}

// SearchHybrid combines semantic vector similarity and lexical token matching.
// alpha (0.0 - 1.0) controls the vector weight (default 0.70 if <= 0 or > 1.0).
// If queryEmbedding is empty or missing, it falls back seamlessly to SearchText.
func (s *Store) SearchHybrid(queryText string, queryEmbedding []float64, k int, alpha float64) []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return nil
	}

	if len(queryEmbedding) == 0 {
		// Pure lexical fallback
		s.mu.RUnlock()
		res := s.SearchText(queryText, k)
		s.mu.RLock()
		return res
	}

	if alpha <= 0 || alpha > 1.0 {
		alpha = 0.70
	}

	tokens := tokenize(queryText)
	phrase := strings.TrimSpace(strings.ToLower(queryText))

	results := make([]Result, 0, len(s.entries))
	for _, e := range s.entries {
		var vecScore float64
		hasVector := len(e.Embedding) > 0
		if hasVector {
			vecScore = cosineSimilarity(queryEmbedding, e.Embedding)
			if vecScore < 0 {
				vecScore = 0
			}
		}

		textScore := computeTextMatchScore(tokens, phrase, e.Text)

		var combinedScore float64
		if !hasVector && len(tokens) > 0 {
			combinedScore = textScore
		} else if len(tokens) == 0 && hasVector {
			combinedScore = vecScore
		} else {
			combinedScore = (alpha * vecScore) + ((1.0 - alpha) * textScore)
		}

		if combinedScore > 0.05 {
			results = append(results, Result{
				Entry: e,
				Score: combinedScore,
			})
		}
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

// UpdateEmbeddings updates the embedding vectors of all matching entries and flushes the store.
func (s *Store) UpdateEmbeddings(embeddings map[string][]float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.entries {
		if vec, ok := embeddings[e.ID]; ok {
			s.entries[i].Embedding = vec
		}
	}
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

var commonStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true, "that": true,
	"from": true, "about": true, "what": true, "how": true, "para": true, "como": true,
	"sobre": true, "esta": true, "este": true, "esto": true, "con": true, "que": true,
	"del": true, "los": true, "las": true, "una": true, "uno": true, "por": true,
	"donde": true, "cual": true, "cuando": true, "hola": true, "help": true, "ayuda": true,
}

func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	var tokens []string
	seen := make(map[string]bool)
	for _, f := range fields {
		f = strings.TrimFunc(f, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-')
		})
		if len(f) >= 2 && !commonStopwords[f] && !seen[f] {
			tokens = append(tokens, f)
			seen[f] = true
		}
	}
	return tokens
}

func computeTextMatchScore(queryTokens []string, queryPhrase string, targetText string) float64 {
	if len(targetText) == 0 {
		return 0
	}
	lowerTarget := strings.ToLower(targetText)

	// Phrase match bonus
	var score float64
	if len(queryPhrase) >= 4 && strings.Contains(lowerTarget, queryPhrase) {
		score += 0.50
	}

	if len(queryTokens) == 0 {
		return score
	}

	targetTokens := tokenize(targetText)
	if len(targetTokens) == 0 {
		return score
	}

	targetMap := make(map[string]int)
	for _, t := range targetTokens {
		targetMap[t]++
	}

	matchedTokens := 0
	for _, q := range queryTokens {
		if count, exists := targetMap[q]; exists {
			matchedTokens++
			if count > 1 {
				score += 0.05
			}
		} else {
			// Substring check for compound words (e.g. postgresql vs postgres)
			for tt := range targetMap {
				if len(q) >= 4 && len(tt) >= 4 && (strings.Contains(tt, q) || strings.Contains(q, tt)) {
					score += 0.15
					matchedTokens++
					break
				}
			}
		}
	}

	overlapRatio := float64(matchedTokens) / float64(len(queryTokens))
	score += overlapRatio * 0.50

	if score > 1.0 {
		score = 1.0
	}
	return score
}
