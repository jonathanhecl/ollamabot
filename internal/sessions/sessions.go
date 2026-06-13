package sessions

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Feedback records a user reaction (thumbs up/down) on a specific message.
type Feedback struct {
	MessageIndex int       `json:"message_index"` // Index into the Messages array
	Reaction     string    `json:"reaction"`      // "positive" or "negative"
	Timestamp    time.Time `json:"timestamp"`
}

// Session holds a persisted conversation.
// Messages are stored in a separate file within the session folder.
type Session struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Model         string            `json:"model"`
	Messages      []json.RawMessage `json:"messages,omitempty"`
	Feedback      []Feedback        `json:"feedback,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	GoalObjective string            `json:"goal_objective,omitempty"`
	GoalStatus    string            `json:"goal_status,omitempty"`    // "active", "paused", "completed", "failed", or ""
	GoalReasoning string            `json:"goal_reasoning,omitempty"` // last evaluator reasoning
}

// IsEmpty returns true if the session contains no messages, no active goals, and no feedback, AND has a default/empty title.
func (s Session) IsEmpty() bool {
	hasContent := len(s.Messages) > 0 || s.GoalObjective != "" || len(s.Feedback) > 0
	if hasContent {
		return false
	}
	return IsDefaultTitle(s.Title)
}

// IsDefaultTitle returns true if the title is empty or matches a default placeholder.
func IsDefaultTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" || title == "New session" || title == "Empty Session" {
		return true
	}
	// Check for Telegram Chat (chatID) default title
	if strings.HasPrefix(title, "Telegram Chat (") && strings.HasSuffix(title, ")") {
		numPart := title[len("Telegram Chat (") : len(title)-1]
		if _, err := strconv.ParseInt(numPart, 10, 64); err == nil {
			return true
		}
	}
	return false
}

// Step represents a single step inside an assistant turn (thinking, tool call, tool result, image progress).
type Step struct {
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	GenID     string `json:"genID,omitempty"`
	Content   string `json:"content,omitempty"`
	ImageURL  string `json:"imageURL,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Status    string `json:"status,omitempty"`
	Call      any    `json:"call,omitempty"` // for tool_call steps with full call object
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// Metrics mirrors Ollama performance metrics stored per assistant turn.
type Metrics struct {
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

// RawMsg is the shape of messages as received from/sent to the frontend.
type RawMsg struct {
	Role           string            `json:"role"`
	Content        string            `json:"content,omitempty"`
	Thinking       string            `json:"thinking,omitempty"`
	Name           string            `json:"name,omitempty"`
	Timestamp      string            `json:"timestamp,omitempty"`
	Model          string            `json:"model,omitempty"`
	Channel        string            `json:"channel,omitempty"`
	Type           string            `json:"type,omitempty"`
	Images         []string          `json:"images,omitempty"`
	AttachmentRefs []string          `json:"attachment_refs,omitempty"`
	ImageKinds     []string          `json:"image_kinds,omitempty"`
	Attachments    []AttachmentMeta  `json:"attachments,omitempty"`
	ToolCalls      []json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults    []json.RawMessage `json:"tool_results,omitempty"`
	Steps          []Step            `json:"steps,omitempty"`
	Metrics        *Metrics          `json:"metrics,omitempty"`
}

type AttachmentMeta struct {
	Name          string `json:"name,omitempty"`
	Mime          string `json:"mime,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Data          string `json:"data,omitempty"`
	URL           string `json:"url,omitempty"`
	Transcription string `json:"transcription,omitempty"`
	Description   string `json:"description,omitempty"`
	ProcessedBy   string `json:"processed_by,omitempty"`
	ProcessedAt   string `json:"processed_at,omitempty"`
	Unreadable    bool   `json:"unreadable,omitempty"`
	Path          string `json:"path,omitempty"`
	Size          int64  `json:"size,omitempty"`
}

var (
	emptySessionsMu sync.RWMutex
	emptySessions   = make(map[string]Session)

	cacheMu      sync.RWMutex
	sessionCache = make(map[string]map[string]*Session)
	cacheLoaded  = make(map[string]bool)
)

func cloneSession(s Session) Session {
	var msgsCopy []json.RawMessage
	if s.Messages != nil {
		msgsCopy = make([]json.RawMessage, len(s.Messages))
		for i, m := range s.Messages {
			msgsCopy[i] = make(json.RawMessage, len(m))
			copy(msgsCopy[i], m)
		}
	}
	var fbCopy []Feedback
	if s.Feedback != nil {
		fbCopy = make([]Feedback, len(s.Feedback))
		copy(fbCopy, s.Feedback)
	}
	return Session{
		ID:            s.ID,
		Title:         s.Title,
		Model:         s.Model,
		Messages:      msgsCopy,
		Feedback:      fbCopy,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
		GoalObjective: s.GoalObjective,
		GoalStatus:    s.GoalStatus,
		GoalReasoning: s.GoalReasoning,
	}
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
	return &Store{
		dir: sessionsPath,
	}
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
	cacheMu.Lock()
	loaded := cacheLoaded[s.dir]
	if !loaded {
		if sessionCache[s.dir] == nil {
			sessionCache[s.dir] = make(map[string]*Session)
		}
		entries, err := os.ReadDir(s.dir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				sess, err := s.readMeta(e.Name())
				if err == nil {
					sess.Messages = nil
					sessionCache[s.dir][e.Name()] = &sess
				}
			}
			cacheLoaded[s.dir] = true
		}
	}

	var list []Session
	for _, cachedPtr := range sessionCache[s.dir] {
		cloned := cloneSession(*cachedPtr)
		cloned.Messages = nil
		list = append(list, cloned)
	}
	cacheMu.Unlock()

	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	return list, nil
}

// Get loads a full session including messages with base64 images restored.
func (s *Store) Get(id string) (Session, error) {
	emptySessionsMu.RLock()
	sess, ok := emptySessions[id]
	emptySessionsMu.RUnlock()
	if ok {
		return cloneSession(sess), nil
	}

	cacheMu.Lock()
	if sessionCache[s.dir] == nil {
		sessionCache[s.dir] = make(map[string]*Session)
	}
	cached, found := sessionCache[s.dir][id]
	if found && cached.Messages != nil {
		cloned := cloneSession(*cached)
		cacheMu.Unlock()
		return cloned, nil
	}
	cacheMu.Unlock()

	var metaSess Session
	metaPath := s.sessionMetaPath(id)
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return metaSess, fmt.Errorf("session not found")
	}
	if err := json.Unmarshal(metaData, &metaSess); err != nil {
		return metaSess, err
	}

	msgsPath := s.messagesPath(id)
	msgsData, err := os.ReadFile(msgsPath)
	if err == nil {
		var rawMessages []json.RawMessage
		if err := json.Unmarshal(msgsData, &rawMessages); err == nil {
			metaSess.Messages, err = s.loadMessagesWithAttachments(id, rawMessages)
			if err != nil {
				return metaSess, err
			}
		}
	}

	cacheMu.Lock()
	sessionCache[s.dir][id] = &metaSess
	cloned := cloneSession(metaSess)
	cacheMu.Unlock()

	return cloned, nil
}

// Save persists a session, extracting base64 images into the attachments folder.
func (s *Store) Save(sess Session) error {
	sess.UpdatedAt = time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = sess.UpdatedAt
	}

	if sess.IsEmpty() {
		emptySessionsMu.Lock()
		emptySessions[sess.ID] = sess
		emptySessionsMu.Unlock()

		cacheMu.Lock()
		if sessionCache[s.dir] != nil {
			delete(sessionCache[s.dir], sess.ID)
		}
		cacheMu.Unlock()

		// Clean up from disk if it was previously saved
		_ = os.RemoveAll(s.sessionDir(sess.ID))
		return nil
	}

	emptySessionsMu.Lock()
	delete(emptySessions, sess.ID)
	emptySessionsMu.Unlock()

	// Update cache
	cacheMu.Lock()
	if sessionCache[s.dir] == nil {
		sessionCache[s.dir] = make(map[string]*Session)
	}
	cachedSess := cloneSession(sess)
	sessionCache[s.dir][sess.ID] = &cachedSess
	cacheMu.Unlock()

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
	emptySessionsMu.Lock()
	delete(emptySessions, id)
	emptySessionsMu.Unlock()

	cacheMu.Lock()
	if sessionCache[s.dir] != nil {
		delete(sessionCache[s.dir], id)
	}
	cacheMu.Unlock()

	return os.RemoveAll(s.sessionDir(id))
}

// SaveFeedback appends a feedback entry to an existing session's metadata.
func (s *Store) SaveFeedback(id string, fb Feedback) error {
	sess, err := s.Get(id)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}
	sess.Feedback = append(sess.Feedback, fb)
	return s.Save(sess)
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

// attachmentStorage represents a saved attachment with its metadata and data
type attachmentStorage struct {
	Name          string `json:"name"`
	Mime          string `json:"mime"`
	Kind          string `json:"kind"`
	Data          string `json:"data"` // base64 encoded
	Transcription string `json:"transcription,omitempty"`
	Description   string `json:"description,omitempty"`
	ProcessedBy   string `json:"processed_by,omitempty"`
	ProcessedAt   string `json:"processed_at,omitempty"`
	Unreadable    bool   `json:"unreadable,omitempty"`
}

// extractAttachments decodes base64 images in messages and saves them as
// binary files in the session's attachments folder. It replaces Images with
// AttachmentRefs in the returned messages. Also handles Attachments from frontend.
func (s *Store) extractAttachments(id string, messages []json.RawMessage) ([]json.RawMessage, error) {
	var out []json.RawMessage
	attDir := s.attachmentsDir(id)

	for mi, raw := range messages {
		var msg RawMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			out = append(out, raw)
			continue
		}

		var refs []string
		attachmentIndex := 0
		// Track already-extracted base64 data to avoid duplicates between Images and Attachments
		extractedData := make(map[string]bool)

		// Process the Attachments field FIRST: it carries the richest metadata
		// (name, mime, transcription). Duplicated base64 data in Images is then
		// skipped by the dedupe map, so metadata is never lost.
		for _, att := range msg.Attachments {
			if att.Data == "" || extractedData[att.Data] {
				continue
			}
			extractedData[att.Data] = true
			ref := fmt.Sprintf("%d_%d.json", mi, attachmentIndex)
			path := filepath.Join(attDir, ref)
			storage := attachmentStorage{
				Name:          att.Name,
				Mime:          att.Mime,
				Kind:          att.Kind,
				Data:          att.Data,
				Transcription: att.Transcription,
				Description:   att.Description,
				ProcessedBy:   att.ProcessedBy,
				ProcessedAt:   att.ProcessedAt,
				Unreadable:    att.Unreadable,
			}
			storageData, err := json.Marshal(storage)
			if err != nil {
				continue
			}
			if err := os.WriteFile(path, storageData, 0o644); err != nil {
				continue
			}
			refs = append(refs, ref)
			attachmentIndex++
		}

		// Process traditional Images field (array of base64 strings)
		// Try to infer kind from ImageKinds or default to "image"
		for ii, b64 := range msg.Images {
			if b64 == "" || extractedData[b64] {
				continue
			}
			extractedData[b64] = true
			ref := fmt.Sprintf("%d_%d.json", mi, attachmentIndex)
			path := filepath.Join(attDir, ref)
			kind := "image"
			if ii < len(msg.ImageKinds) {
				kind = msg.ImageKinds[ii]
			}
			mime := "image/png"
			if kind == "audio" {
				mime = "audio/wav"
			}
			storage := attachmentStorage{
				Name: fmt.Sprintf("attachment_%d_%d", mi, ii),
				Mime: mime,
				Kind: kind,
				Data: b64,
			}
			storageData, err := json.Marshal(storage)
			if err != nil {
				continue
			}
			if err := os.WriteFile(path, storageData, 0o644); err != nil {
				continue
			}
			refs = append(refs, ref)
			attachmentIndex++
		}

		// Only update message if we extracted any attachments
		if len(refs) > 0 {
			msg.Images = nil
			msg.Attachments = nil
			msg.ImageKinds = nil
			msg.AttachmentRefs = refs
			updated, err := json.Marshal(msg)
			if err != nil {
				out = append(out, raw)
			} else {
				out = append(out, updated)
			}
		} else {
			out = append(out, raw)
		}
	}
	return out, nil
}

// loadMessagesWithAttachments reads referenced attachment files and restores
// base64 Images and Attachments in the returned messages.
func (s *Store) loadMessagesWithAttachments(id string, messages []json.RawMessage) ([]json.RawMessage, error) {
	var out []json.RawMessage
	attDir := s.attachmentsDir(id)

	for _, raw := range messages {
		var msg RawMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			out = append(out, raw)
			continue
		}
		if len(msg.AttachmentRefs) == 0 {
			out = append(out, raw)
			continue
		}

		var images []string
		var attachments []AttachmentMeta
		var imageKinds []string
		for _, ref := range msg.AttachmentRefs {
			path := filepath.Join(attDir, ref)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			// Try to parse as JSON (new format with metadata)
			var storage attachmentStorage
			if err := json.Unmarshal(data, &storage); err == nil {
				// New JSON format with metadata
				images = append(images, storage.Data)
				imageKinds = append(imageKinds, storage.Kind)
				attachments = append(attachments, AttachmentMeta{
					Name:          storage.Name,
					Mime:          storage.Mime,
					Kind:          storage.Kind,
					Data:          storage.Data,
					URL:           "",
					Transcription: storage.Transcription,
					Description:   storage.Description,
					ProcessedBy:   storage.ProcessedBy,
					ProcessedAt:   storage.ProcessedAt,
					Unreadable:    storage.Unreadable,
				})
			} else {
				// Legacy binary format - assume image
				b64 := base64.StdEncoding.EncodeToString(data)
				images = append(images, b64)
				imageKinds = append(imageKinds, "image")
				attachments = append(attachments, AttachmentMeta{
					Name: "attachment.bin",
					Mime: "application/octet-stream",
					Kind: "image",
					Data: b64,
					URL:  "",
				})
			}
		}
		msg.Images = images
		msg.ImageKinds = imageKinds
		msg.Attachments = attachments
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

// idCounter ensures GenerateID produces unique values even when called
// within the same nanosecond (e.g., on low-resolution timer systems like Windows).
var idCounter atomic.Uint64

// GenerateID creates a time-based unique identifier.
func GenerateID() string {
	seq := idCounter.Add(1)
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), seq)
}

var (
	listenersMu sync.Mutex
	listeners   = make(map[chan string]bool)
)

// Subscribe returns a channel that receives session IDs when they are updated.
func Subscribe() chan string {
	listenersMu.Lock()
	defer listenersMu.Unlock()
	ch := make(chan string, 10)
	listeners[ch] = true
	return ch
}

// Unsubscribe removes a channel from the list of update listeners.
func Unsubscribe(ch chan string) {
	listenersMu.Lock()
	defer listenersMu.Unlock()
	delete(listeners, ch)
	close(ch)
}

// NotifyUpdate broadcasts a session update event to all subscribed listeners.
func NotifyUpdate(sessionID string) {
	listenersMu.Lock()
	defer listenersMu.Unlock()
	for ch := range listeners {
		select {
		case ch <- sessionID:
		default:
		}
	}
}
