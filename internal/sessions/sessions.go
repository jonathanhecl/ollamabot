package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Session holds a persisted conversation. Messages are stored as raw JSON
// so the frontend can evolve its schema without backend changes.
type Session struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Model     string            `json:"model"`
	Messages  []json.RawMessage `json:"messages,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Store persists sessions as JSON files inside workspace/sessions.
type Store struct {
	dir string
}

func NewStore(workspace string) *Store {
	dir := filepath.Join(workspace, "sessions")
	_ = os.MkdirAll(dir, 0o755)
	return &Store{dir: dir}
}

// List returns all sessions ordered by updated_at descending.
func (s *Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		sess, err := s.readFile(e.Name())
		if err != nil {
			continue
		}
		sess.Messages = nil // omit messages for listing
		sessions = append(sessions, sess)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// Get loads a full session including messages.
func (s *Store) Get(id string) (Session, error) {
	var sess Session
	path := s.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return sess, fmt.Errorf("session not found")
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return sess, err
	}
	return sess, nil
}

// Save writes or overwrites a session JSON file.
func (s *Store) Save(sess Session) error {
	sess.UpdatedAt = time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = sess.UpdatedAt
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(sess.ID), data, 0o644)
}

// Delete removes a session file.
func (s *Store) Delete(id string) error {
	return os.Remove(s.path(id))
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) readFile(name string) (Session, error) {
	var sess Session
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return sess, err
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return sess, err
	}
	return sess, nil
}

// GenerateID creates a time-based unique identifier.
func GenerateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
