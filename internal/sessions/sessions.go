package sessions

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Session holds a persisted conversation.
// Messages are stored in a separate file within the session folder.
type Session struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Model     string            `json:"model"`
	Messages  []json.RawMessage `json:"messages,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// rawMsg is the shape of messages as received from/sent to the frontend.
type rawMsg struct {
	Role           string            `json:"role"`
	Content        string            `json:"content,omitempty"`
	Thinking       string            `json:"thinking,omitempty"`
	Name           string            `json:"name,omitempty"`
	Images         []string          `json:"images,omitempty"`
	AttachmentRefs []string          `json:"attachment_refs,omitempty"`
	ImageKinds     []string          `json:"image_kinds,omitempty"`
	Attachments    []attachmentMeta  `json:"attachments,omitempty"`
	ToolCalls      []json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults    []json.RawMessage `json:"tool_results,omitempty"`
}

type attachmentMeta struct {
	Name string `json:"name,omitempty"`
	Mime string `json:"mime,omitempty"`
	Kind string `json:"kind,omitempty"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Store persists sessions as folders inside sessionsPath.
// Each session folder contains:
//   - session.json  (metadata)
//   - messages.json (messages with attachment references)
//   - attachments/  (binary files for base64 images)
type Store struct {
	dir string
}

func NewStore(sessionsPath string) *Store {
	_ = os.MkdirAll(sessionsPath, 0o755)
	return &Store{dir: sessionsPath}
}

func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.dir, id)
}

func (s *Store) sessionMetaPath(id string) string {
	return filepath.Join(s.sessionDir(id), "session.json")
}

func (s *Store) messagesPath(id string) string {
	return filepath.Join(s.sessionDir(id), "messages.json")
}

func (s *Store) attachmentsDir(id string) string {
	return filepath.Join(s.sessionDir(id), "attachments")
}

// List returns all sessions ordered by updated_at descending.
func (s *Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sess, err := s.readMeta(e.Name())
		if err != nil {
			continue
		}
		sess.Messages = nil
		sessions = append(sessions, sess)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// Get loads a full session including messages with base64 images restored.
func (s *Store) Get(id string) (Session, error) {
	var sess Session
	metaPath := s.sessionMetaPath(id)
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return sess, fmt.Errorf("session not found")
	}
	if err := json.Unmarshal(metaData, &sess); err != nil {
		return sess, err
	}

	msgsPath := s.messagesPath(id)
	msgsData, err := os.ReadFile(msgsPath)
	if err != nil {
		return sess, nil // no messages yet
	}
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(msgsData, &rawMessages); err != nil {
		return sess, err
	}

	sess.Messages, err = s.loadMessagesWithAttachments(id, rawMessages)
	if err != nil {
		return sess, err
	}
	return sess, nil
}

// Save persists a session, extracting base64 images into the attachments folder.
func (s *Store) Save(sess Session) error {
	sess.UpdatedAt = time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = sess.UpdatedAt
	}

	sd := s.sessionDir(sess.ID)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.attachmentsDir(sess.ID), 0o755); err != nil {
		return err
	}

	// Save metadata without messages
	meta := sess
	meta.Messages = nil
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.sessionMetaPath(sess.ID), metaData, 0o644); err != nil {
		return err
	}

	// Save messages with attachments extracted
	storedMsgs, err := s.extractAttachments(sess.ID, sess.Messages)
	if err != nil {
		return err
	}
	msgsData, err := json.MarshalIndent(storedMsgs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.messagesPath(sess.ID), msgsData, 0o644)
}

// Delete removes a session folder and all its contents.
func (s *Store) Delete(id string) error {
	return os.RemoveAll(s.sessionDir(id))
}

func (s *Store) readMeta(id string) (Session, error) {
	var sess Session
	data, err := os.ReadFile(s.sessionMetaPath(id))
	if err != nil {
		return sess, err
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return sess, err
	}
	return sess, nil
}

// extractAttachments decodes base64 images in messages and saves them as
// binary files in the session's attachments folder. It replaces Images with
// AttachmentRefs in the returned messages.
func (s *Store) extractAttachments(id string, messages []json.RawMessage) ([]json.RawMessage, error) {
	var out []json.RawMessage
	attDir := s.attachmentsDir(id)

	for mi, raw := range messages {
		var msg rawMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			out = append(out, raw)
			continue
		}
		if len(msg.Images) == 0 {
			out = append(out, raw)
			continue
		}

		var refs []string
		for ii, b64 := range msg.Images {
			data, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				continue
			}
			ref := fmt.Sprintf("%d_%d.bin", mi, ii)
			path := filepath.Join(attDir, ref)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				continue
			}
			refs = append(refs, ref)
		}
		msg.Images = nil
		msg.AttachmentRefs = refs
		updated, err := json.Marshal(msg)
		if err != nil {
			out = append(out, raw)
		} else {
			out = append(out, updated)
		}
	}
	return out, nil
}

// loadMessagesWithAttachments reads referenced attachment files and restores
// base64 Images in the returned messages.
func (s *Store) loadMessagesWithAttachments(id string, messages []json.RawMessage) ([]json.RawMessage, error) {
	var out []json.RawMessage
	attDir := s.attachmentsDir(id)

	for _, raw := range messages {
		var msg rawMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			out = append(out, raw)
			continue
		}
		if len(msg.AttachmentRefs) == 0 {
			out = append(out, raw)
			continue
		}

		var images []string
		for _, ref := range msg.AttachmentRefs {
			path := filepath.Join(attDir, ref)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			images = append(images, base64.StdEncoding.EncodeToString(data))
		}
		msg.Images = images
		msg.AttachmentRefs = nil
		updated, err := json.Marshal(msg)
		if err != nil {
			out = append(out, raw)
		} else {
			out = append(out, updated)
		}
	}
	return out, nil
}

// GenerateID creates a time-based unique identifier.
func GenerateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
