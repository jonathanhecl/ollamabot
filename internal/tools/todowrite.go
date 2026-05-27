package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
)

const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusCompleted  = "completed"
	TodoStatusCancelled  = "cancelled"
)

type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoStore struct {
	mu    sync.RWMutex
	items []TodoItem
}

func NewTodoStore() *TodoStore {
	return &TodoStore{}
}

func (s *TodoStore) Snapshot() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

func (s *TodoStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
}

func (s *TodoStore) Apply(merge bool, incoming []TodoItem) []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !merge || len(s.items) == 0 {
		s.items = normaliseAll(incoming)
		out := make([]TodoItem, len(s.items))
		copy(out, s.items)
		return out
	}
	index := make(map[string]int, len(s.items))
	for i, it := range s.items {
		index[it.ID] = i
	}
	for _, it := range incoming {
		it = normaliseOne(it)
		if pos, ok := index[it.ID]; ok {
			cur := s.items[pos]
			if strings.TrimSpace(it.Content) != "" {
				cur.Content = it.Content
			}
			if it.Status != "" {
				cur.Status = it.Status
			}
			s.items[pos] = cur
			continue
		}
		s.items = append(s.items, it)
		index[it.ID] = len(s.items) - 1
	}
	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

func ValidateTodoContent(incoming []TodoItem, merge bool, existing []TodoItem) error {
	existingIDs := map[string]struct{}{}
	if merge {
		for _, it := range existing {
			existingIDs[it.ID] = struct{}{}
		}
	}
	for i, it := range incoming {
		content := strings.TrimSpace(it.Content)
		if content == "" {
			if merge {
				if _, ok := existingIDs[it.ID]; ok {
					continue
				}
			}
			return fmt.Errorf("item %d content must be descriptive text", i)
		}
		if !containsLetter(content) {
			return fmt.Errorf("item %d content must be descriptive text, not a placeholder like %q", i, content)
		}
	}
	return nil
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func DecodeTodos(raw any) ([]TodoItem, error) {
	switch v := raw.(type) {
	case []TodoItem:
		return v, nil
	case []any:
		out := make([]TodoItem, 0, len(v))
		for i, entry := range v {
			m, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("item %d is not an object", i)
			}
			id, _ := m["id"].(string)
			content, _ := m["content"].(string)
			status, _ := m["status"].(string)
			if strings.TrimSpace(id) == "" {
				return nil, fmt.Errorf("item %d missing id", i)
			}
			out = append(out, TodoItem{ID: id, Content: content, Status: status})
		}
		return out, nil
	}
	if s, ok := raw.(string); ok {
		var parsed []TodoItem
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("expected an array of {id, content, status}")
}

func RenderTodosForModel(items []TodoItem) string {
	if len(items) == 0 {
		return "TODO list is empty."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TODOs (%d):\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", statusGlyphASCII(it.Status), it.ID, it.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

func statusGlyphASCII(status string) string {
	switch status {
	case TodoStatusInProgress:
		return "~"
	case TodoStatusCompleted:
		return "x"
	case TodoStatusCancelled:
		return "-"
	default:
		return " "
	}
}

func normaliseOne(it TodoItem) TodoItem {
	switch it.Status {
	case TodoStatusInProgress, TodoStatusCompleted, TodoStatusCancelled, TodoStatusPending:
		// ok
	default:
		it.Status = TodoStatusPending
	}
	return it
}

func normaliseAll(in []TodoItem) []TodoItem {
	out := make([]TodoItem, len(in))
	for i, it := range in {
		out[i] = normaliseOne(it)
	}
	return out
}
