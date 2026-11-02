package device

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// Turn statuses reported to the device. The screen shows stage while the turn
// runs and switches to display_text once it is ready.
const (
	StatusQueued     = "queued"
	StatusProcessing = "processing"
	StatusSpeaking   = "speaking"
	StatusReady      = "ready"
	StatusFailed     = "failed"
	StatusCanceled   = "canceled"
)

const (
	stageThinking = "thinking"
	stageWorking  = "working"
	stageHeard    = "heard you"
	stageSpeaking = "speaking"
	stageDone     = "done"
)

const displayTextLimit = 180

// Turn is one device request: an uploaded audio clip, its agent turn and the
// spoken reply. Fields are guarded by turnStore.mu.
type Turn struct {
	ID          string
	DeviceID    string
	SessionID   string
	Status      string
	Stage       string
	Activity    string
	DisplayText string
	Transcript  string
	Error       string

	AudioPath     string
	AudioMIME     string
	AudioFormat   string
	AudioBytes    int
	AudioRate     int
	AudioChannels int
	AudioBits     int

	CreatedAt time.Time
	UpdatedAt time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// TurnView is the JSON snapshot sent to the device.
type TurnView struct {
	ID            string    `json:"turn_id"`
	SessionID     string    `json:"session_id"`
	Status        string    `json:"status"`
	Stage         string    `json:"stage,omitempty"`
	Activity      string    `json:"activity,omitempty"`
	DisplayText   string    `json:"display_text,omitempty"`
	Transcript    string    `json:"transcript,omitempty"`
	AudioReady    bool      `json:"audio_ready"`
	AudioURL      string    `json:"audio_url,omitempty"`
	AudioMIME     string    `json:"audio_mime,omitempty"`
	AudioFormat   string    `json:"audio_format,omitempty"`
	AudioBytes    int       `json:"audio_bytes,omitempty"`
	AudioRate     int       `json:"audio_rate,omitempty"`
	AudioChannels int       `json:"audio_channels,omitempty"`
	AudioBits     int       `json:"audio_bits,omitempty"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// audioPath is the on-disk reply file; it stays out of the JSON payload.
	audioPath string
}

func (t *Turn) view() TurnView {
	view := TurnView{
		ID:            t.ID,
		SessionID:     t.SessionID,
		Status:        t.Status,
		Stage:         t.Stage,
		Activity:      t.Activity,
		DisplayText:   t.DisplayText,
		Transcript:    t.Transcript,
		AudioMIME:     t.AudioMIME,
		AudioFormat:   t.AudioFormat,
		AudioBytes:    t.AudioBytes,
		AudioRate:     t.AudioRate,
		AudioChannels: t.AudioChannels,
		AudioBits:     t.AudioBits,
		Error:         t.Error,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		audioPath:     t.AudioPath,
	}
	if t.AudioPath != "" && t.AudioBytes > 0 {
		view.AudioReady = true
		view.AudioURL = "/api/device/turns/" + t.ID + "/audio"
	}
	return view
}

// turnStore keeps turns in memory. Audio lives on disk with the session, so a
// restart only drops in-flight turns: a device simply speaks again.
type turnStore struct {
	mu     sync.Mutex
	turns  map[string]*Turn
	active map[string]string
	ttl    time.Duration
}

func newTurnStore(ttl time.Duration) *turnStore {
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return &turnStore{turns: map[string]*Turn{}, active: map[string]string{}, ttl: ttl}
}

func (s *turnStore) create(turn *Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.turns[turn.ID] = turn
	s.active[turn.DeviceID] = turn.ID
}

func (s *turnStore) update(id string, fn func(*Turn)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turns[id]
	if turn == nil {
		return
	}
	fn(turn)
	turn.UpdatedAt = time.Now()
}

func (s *turnStore) view(id string) (TurnView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turns[id]
	if turn == nil {
		return TurnView{}, false
	}
	return turn.view(), true
}

func (s *turnStore) activeView(deviceID string) (TurnView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.active[deviceID]
	if id == "" {
		return TurnView{}, false
	}
	turn := s.turns[id]
	if turn == nil {
		return TurnView{}, false
	}
	return turn.view(), true
}

func (s *turnStore) raw(id string) *Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns[id]
}

func (s *turnStore) finish(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turns[id]
	if turn == nil {
		return
	}
	if s.active[turn.DeviceID] == id {
		delete(s.active, turn.DeviceID)
	}
	select {
	case <-turn.done:
	default:
		close(turn.done)
	}
}

// cancelAll stops every turn and waits for the goroutines to leave, so callers
// (tests, shutdown) do not delete their working directories under a live turn.
func (s *turnStore) cancelAll(wait time.Duration) {
	s.mu.Lock()
	turns := make([]*Turn, 0, len(s.turns))
	for _, turn := range s.turns {
		turns = append(turns, turn)
	}
	s.mu.Unlock()
	for _, turn := range turns {
		if turn.cancel != nil {
			turn.cancel()
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, turn := range turns {
		select {
		case <-turn.done:
		case <-timer.C:
			return
		}
	}
}

func (s *turnStore) pruneLocked() {
	cutoff := time.Now().Add(-s.ttl)
	for id, turn := range s.turns {
		if turn.Status == StatusQueued || turn.Status == StatusProcessing || turn.Status == StatusSpeaking {
			continue
		}
		if turn.UpdatedAt.Before(cutoff) {
			delete(s.turns, id)
		}
	}
}

// audioContentTypes maps the content types a device may send to the canonical
// type reported back and stored. An absent header defaults to WAV.
var audioContentTypes = map[string]string{
	"":                         "audio/wav",
	"audio/wav":                "audio/wav",
	"audio/x-wav":              "audio/wav",
	"audio/wave":               "audio/wav",
	"audio/pcm":                "audio/pcm",
	"audio/x-pcm":              "audio/pcm",
	"audio/ogg":                "audio/ogg",
	"audio/mp3":                "audio/mpeg",
	"audio/mpeg":               "audio/mpeg",
	"audio/mp4":                "audio/mp4",
	"audio/m4a":                "audio/mp4",
	"audio/x-m4a":              "audio/mp4",
	"application/octet-stream": "audio/wav",
}

func canonicalAudioType(contentType string) (string, bool) {
	mediaType := strings.TrimSpace(strings.Split(contentType, ";")[0])
	mediaType = strings.ToLower(mediaType)
	canonical, ok := audioContentTypes[mediaType]
	return canonical, ok
}

type createTurnResponse struct {
	TurnID    string `json:"turn_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

func (c *Channel) handleCreateTurn(w http.ResponseWriter, r *http.Request) {
	deviceID, err := deviceID(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	deps := c.deps()
	cfg := deps.ConfigMgr.Get()
	if strings.TrimSpace(cfg.OllamaModelSpeech) == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "no speech model configured")
		return
	}
	audioType, ok := canonicalAudioType(r.Header.Get("Content-Type"))
	if !ok {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported Content-Type: send audio/wav, audio/pcm, audio/ogg or audio/mpeg")
		return
	}

	maxBytes := int64(cfg.DeviceMaxAudioMB) << 20
	if maxBytes <= 0 {
		maxBytes = 15 << 20
	}
	if r.ContentLength > maxBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "audio payload is larger than DEVICE_MAX_AUDIO_MB")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "audio payload is larger than DEVICE_MAX_AUDIO_MB")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "could not read audio payload")
		return
	}
	if len(body) < 128 {
		writeAPIError(w, http.StatusBadRequest, "audio payload is too small")
		return
	}

	if active, ok := c.turns.activeView(deviceID); ok {
		if r.Header.Get("X-Device-Interrupt") != "1" {
			writeJSON(w, http.StatusConflict, active)
			return
		}
		if err := c.stopActiveTurn(deviceID, 10*time.Second); err != nil {
			writeAPIError(w, http.StatusConflict, err.Error())
			return
		}
	}

	sessionID, err := c.ensureSession(deviceID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not create the device session")
		return
	}

	timeout := time.Duration(cfg.DeviceTurnTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	now := time.Now()
	turn := &Turn{
		ID:        sessions.GenerateID(),
		DeviceID:  deviceID,
		SessionID: sessionID,
		Status:    StatusQueued,
		Stage:     "queued",
		CreatedAt: now,
		UpdatedAt: now,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	c.turns.create(turn)
	go c.runTurn(ctx, cancel, turn.ID, sessionID, audioType, body)

	writeJSON(w, http.StatusAccepted, createTurnResponse{
		TurnID:    turn.ID,
		SessionID: sessionID,
		Status:    StatusQueued,
	})
}

// stopActiveTurn cancels the turn running for a device and waits for its
// goroutine, so two turns never write the same session concurrently.
func (c *Channel) stopActiveTurn(deviceID string, wait time.Duration) error {
	active, ok := c.turns.activeView(deviceID)
	if !ok {
		return nil
	}
	turn := c.turns.raw(active.ID)
	if turn == nil {
		return nil
	}
	if turn.cancel != nil {
		turn.cancel()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-turn.done:
		return nil
	case <-timer.C:
		return errors.New("previous turn is still shutting down; retry shortly")
	}
}

func (c *Channel) handleGetTurn(w http.ResponseWriter, r *http.Request) {
	deviceID, err := deviceID(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	turn, ok := c.turns.view(r.PathValue("id"))
	if !ok || !c.turnBelongsTo(turn.ID, deviceID) {
		writeAPIError(w, http.StatusNotFound, "turn not found")
		return
	}
	writeJSON(w, http.StatusOK, turn)
}

func (c *Channel) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	deviceID, err := deviceID(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	if !c.turnBelongsTo(id, deviceID) {
		writeAPIError(w, http.StatusNotFound, "turn not found")
		return
	}
	if turn := c.turns.raw(id); turn != nil && turn.cancel != nil {
		turn.cancel()
	}
	turn, ok := c.turns.view(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "turn not found")
		return
	}
	writeJSON(w, http.StatusOK, turn)
}

func (c *Channel) turnBelongsTo(id, deviceID string) bool {
	turn := c.turns.raw(id)
	return turn != nil && turn.DeviceID == deviceID
}

type newSessionResponse struct {
	SessionID string `json:"session_id"`
}

func (c *Channel) handleNewSession(w http.ResponseWriter, r *http.Request) {
	deviceID, err := deviceID(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := c.stopActiveTurn(deviceID, 10*time.Second); err != nil {
		writeAPIError(w, http.StatusConflict, err.Error())
		return
	}
	sessionID, err := c.createSession(deviceID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not create the device session")
		return
	}
	writeJSON(w, http.StatusCreated, newSessionResponse{SessionID: sessionID})
}

// ensureSession returns the device conversation, creating it on first contact.
func (c *Channel) ensureSession(deviceID string) (string, error) {
	deps := c.deps()
	links := c.links(deps)
	if id := links.Get(deviceID); id != "" {
		if sess, err := deps.SessionStore.Get(id); err == nil {
			// Self-heal legacy titles on contact as well: a session created by an
			// older build is renamed even if the startup sweep already ran.
			c.normalizeSessionTitle(deps, sess)
			return id, nil
		}
	}
	return c.createSession(deviceID)
}

func (c *Channel) createSession(deviceID string) (string, error) {
	deps := c.deps()
	cfg := deps.ConfigMgr.Get()
	sess := sessions.Session{
		ID:    sessions.GenerateID(),
		Title: "Device " + deviceID,
		Model: config.ResolveModel(cfg, config.ModelRoleMain),
	}
	if err := deps.SessionStore.Save(sess); err != nil {
		return "", err
	}
	sessions.NotifyUpdate(sess.ID)
	if err := c.links(deps).Set(deviceID, sess.ID); err != nil {
		log.Printf("[device] could not persist the session link for %q: %v", deviceID, err)
	}
	return sess.ID, nil
}

// migrateLegacyTitles rewrites the device session titles created by older
// builds ("Device epaper-<id>") to the current "Device <id>" form. It runs once
// when the channel mounts, so existing devices do not keep the retired wording
// in the web UI and in the session list.
func (c *Channel) migrateLegacyTitles() {
	c.migrateOnce.Do(func() {
		deps := c.deps()
		if deps.SessionStore == nil {
			return
		}
		list, err := deps.SessionStore.List()
		if err != nil {
			log.Printf("[device] could not list sessions to rename legacy titles: %v", err)
			return
		}
		for _, entry := range list {
			title := normalizeDeviceTitle(entry.Title)
			if title == "" {
				continue
			}
			sess, err := deps.SessionStore.Get(entry.ID)
			if err != nil {
				continue
			}
			previous := sess.Title
			sess.Title = title
			if err := deps.SessionStore.Save(sess); err != nil {
				log.Printf("[device] could not rename session %s: %v", entry.ID, err)
				continue
			}
			log.Printf("[device] renamed legacy session title %q -> %q", previous, title)
			sessions.NotifyUpdate(entry.ID)
		}
	})
}

// normalizeDeviceTitle converts a legacy device session title to the current
// form and returns "" when the title needs no change.
func normalizeDeviceTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if !strings.HasPrefix(strings.ToLower(trimmed), "device ") {
		return ""
	}
	rest := strings.TrimSpace(trimmed[len("device "):])
	if !strings.HasPrefix(strings.ToLower(rest), "epaper") {
		return ""
	}
	rest = strings.TrimSpace(rest[len("epaper"):])
	rest = strings.TrimLeft(rest, "-–—: ")
	if rest == "" {
		return "Device"
	}
	return "Device " + rest
}

// normalizeSessionTitle rewrites a stored legacy device title in place.
func (c *Channel) normalizeSessionTitle(deps Deps, sess sessions.Session) {
	title := normalizeDeviceTitle(sess.Title)
	if title == "" {
		return
	}
	sess.Title = title
	if err := deps.SessionStore.Save(sess); err != nil {
		log.Printf("[device] could not rename session %s: %v", sess.ID, err)
		return
	}
	log.Printf("[device] renamed legacy session title %q -> %q", sess.Title, title)
	sessions.NotifyUpdate(sess.ID)
}

// links returns the persisted device → session map, cached per sessions path.
func (c *Channel) links(deps Deps) *sessionLinks {
	path := deviceLinksFile
	if deps.SessionStore != nil {
		path = deps.SessionStore.Dir() + "/" + deviceLinksFile
	}
	c.linksMu.Lock()
	defer c.linksMu.Unlock()
	if c.sessionLinks == nil {
		c.sessionLinks = map[string]*sessionLinks{}
	}
	if existing := c.sessionLinks[path]; existing != nil {
		return existing
	}
	created := loadSessionLinks(path)
	c.sessionLinks[path] = created
	return created
}

// runTurn executes the engine turn and records progress for the device poller.
func (c *Channel) runTurn(ctx context.Context, cancel context.CancelFunc, turnID, sessionID, audioType string, audio []byte) {
	defer cancel()
	defer c.turns.finish(turnID)
	c.turns.update(turnID, func(turn *Turn) {
		turn.Status = StatusProcessing
		turn.Stage = stageThinking
	})

	deps := c.deps()
	history := append(engine.LoadSessionRawMessages(deps.SessionStore, sessionID), userAudioMessage(turnID, audioType, audio))
	if err := engine.SaveRawMessages(deps.SessionStore, sessionID, history); err != nil {
		log.Printf("[device] could not persist the incoming audio for session %s: %v", sessionID, err)
	}

	result, err := engine.ProcessTurn(ctx, c.engineDeps(turnID), engine.TurnRequest{
		SessionID:   sessionID,
		Channel:     ChannelName,
		Messages:    engine.MediaMessagesFromRaw(history),
		BaseHistory: history,
	})
	if err != nil {
		// Internal failures (loop guard, model errors, tool crashes) stay in the
		// log: the device only gets a short sentence it can show and speak.
		log.Printf("[device] turn %s failed for session %s: %v", turnID, sessionID, err)
		status := StatusFailed
		message := "I could not complete that request."
		if ctx.Err() != nil {
			status = StatusCanceled
			message = "Turn canceled."
		}
		c.turns.update(turnID, func(turn *Turn) {
			turn.Status = status
			turn.Stage = status
			turn.Error = message
		})
		return
	}

	if ctx.Err() != nil {
		c.turns.update(turnID, func(turn *Turn) {
			turn.Status = StatusCanceled
			turn.Stage = StatusCanceled
			turn.Error = "turn canceled"
		})
		return
	}

	answer := strings.TrimSpace(result.FinalAnswer)
	c.turns.update(turnID, func(turn *Turn) {
		turn.Status = StatusReady
		turn.Stage = stageDone
		turn.DisplayText = screenText(answer, displayTextLimit)
		if answer == "" {
			turn.DisplayText = "No reply."
		}
	})
}

// userAudioMessage turns the uploaded recording into a regular user message, so
// the session timeline shows the audio with its transcription, the same way the
// web and Telegram channels do.
func userAudioMessage(turnID, audioType string, audio []byte) sessions.RawMsg {
	encoded := base64.StdEncoding.EncodeToString(audio)
	return sessions.RawMsg{
		Role:       "user",
		Timestamp:  time.Now().Format(time.RFC3339),
		Channel:    ChannelName,
		Images:     []string{encoded},
		ImageKinds: []string{"audio"},
		Attachments: []sessions.AttachmentMeta{{
			Name: "device_audio_" + turnID + audioExtension(audioType),
			Mime: audioType,
			Kind: "audio",
			Data: encoded,
			Size: int64(len(audio)),
		}},
	}
}

func audioExtension(audioType string) string {
	switch audioType {
	case "audio/pcm":
		return ".pcm"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	default:
		return ".wav"
	}
}

// engineDeps wires the shared engine for a device turn. The device never
// receives an ApprovalService: approving a risky tool means pressing a button
// in the web UI, and nobody is there. A deny-all handler keeps the risk gate
// active, so risky tools fail instead of running unsupervised.
func (c *Channel) engineDeps(turnID string) engine.Deps {
	deps := c.deps()
	return engine.Deps{
		ConfigMgr:        deps.ConfigMgr,
		Client:           deps.Client,
		SessionStore:     deps.SessionStore,
		MemoryStore:      deps.MemoryStore,
		CachePath:        deps.CachePath,
		MCPManager:       deps.MCPManager,
		SchedulerManager: deps.SchedulerManager,
		ApprovalHandler:  denyApprovals{},
		// Device turns have no transport to stream to, but the session still
		// needs the recorder so the web timeline receives the tool steps,
		// timings and metrics of a normal turn.
		StreamHandlerFactory: func(recorder *sessions.Recorder, model string) agent.StreamHandler {
			return &turnStageHandler{
				StreamHandler: agent.NewRecorderStreamHandler(recorder),
				update: func(fn func(*Turn)) {
					c.turns.update(turnID, fn)
				},
			}
		},
		OnFinalReply: func(ctx context.Context, reply engine.FinalReplyContext) {
			c.turns.update(turnID, func(turn *Turn) {
				turn.Status = StatusSpeaking
				turn.Stage = stageSpeaking
			})
			audio, err := c.synthesizeReply(ctx, deps.ConfigMgr.Get(), deps, reply, turnID)
			if err != nil {
				log.Printf("[device] spoken reply unavailable for session %s: %v", reply.SessionID, err)
				return
			}
			c.turns.update(turnID, func(turn *Turn) {
				turn.AudioPath = audio.Path
				turn.AudioMIME = audio.MIME
				turn.AudioFormat = "wav"
				turn.AudioBytes = audio.Bytes
				turn.AudioRate = audio.Rate
				turn.AudioChannels = audio.Channels
				turn.AudioBits = audio.Bits
			})
		},
		OnMediaResolved: func(resolved router.ResolveResult) {
			for _, attachment := range resolved.Attachments {
				if attachment.Kind == "audio" && strings.TrimSpace(attachment.Transcription) != "" {
					transcription := attachment.Transcription
					c.turns.update(turnID, func(turn *Turn) {
						turn.Transcript = transcription
						turn.Stage = stageHeard
					})
					return
				}
			}
		},
	}
}

// turnStageHandler keeps the device informed while the agent works: the stage
// moves to "working" with the running tool name and returns to "thinking" once
// the tool finishes, so the client can show progress during a long tool loop.
// Everything else is forwarded to the recorder handler unchanged.
type turnStageHandler struct {
	agent.StreamHandler
	update func(func(*Turn))
}

func (h *turnStageHandler) OnThinking(delta string) {
	h.update(func(t *Turn) {
		if t.Stage != stageWorking {
			t.Stage = stageThinking
		}
	})
	h.StreamHandler.OnThinking(delta)
}

func (h *turnStageHandler) OnToolStart(name string, args any, source string) {
	h.update(func(t *Turn) {
		t.Stage = stageWorking
		t.Activity = name
	})
	h.StreamHandler.OnToolStart(name, args, source)
}

func (h *turnStageHandler) OnToolResult(name, result, source string) {
	h.update(func(t *Turn) {
		if t.Stage == stageWorking && t.Activity == name {
			t.Stage = stageThinking
			t.Activity = ""
		}
	})
	h.StreamHandler.OnToolResult(name, result, source)
}

// denyApprovals answers every risky-tool request with a denial. The tool layer
// turns that into "Execution denied by user." so the model can explain itself
// out loud instead of hanging.
type denyApprovals struct{}

func (denyApprovals) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	log.Printf("[device] risky tool %q denied: the device cannot approve tools", toolName)
	return false, nil
}

var _ tools.ApprovalHandler = denyApprovals{}
