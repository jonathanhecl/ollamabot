// Package device implements the HTTP channel used by audio devices (a
// microphone, a speaker and a screen, or just a speaker) that talk to
// ollamabot audio-to-audio.
//
// The channel is transport only: it authenticates the device, accepts audio,
// starts a normal engine turn and exposes its progress and spoken reply. All
// conversation logic stays in internal/engine, exactly like the web and
// Telegram channels.
package device

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/mcp"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// ChannelName is the channel identifier recorded on session messages produced
// by device turns.
const ChannelName = "device"

// Deps is the dependency set the web server exposes to the device channel.
// The values are read per request so wiring that happens after construction
// (MCP, scheduler) is visible without re-creating the channel.
type Deps struct {
	ConfigMgr        *config.Manager
	Client           *ollama.Client
	SessionStore     *sessions.Store
	MemoryStore      *memory.Store
	CachePath        string
	MCPManager       *mcp.Manager
	SchedulerManager tools.SchedulerService
}

// Channel serves the device API. Mount it under /api/device/ on the web mux.
type Channel struct {
	deps  func() Deps
	turns *turnStore

	linksMu      sync.Mutex
	sessionLinks map[string]*sessionLinks

	migrateOnce sync.Once
}

// NewChannel builds a device channel over a live dependency provider.
func NewChannel(deps func() Deps) *Channel {
	return &Channel{
		deps:  deps,
		turns: newTurnStore(2 * time.Hour),
	}
}

// Mount registers every device route. The device channel authenticates with its
// own token, so the web password middleware must skip /api/device/.
func (c *Channel) Mount(mux *http.ServeMux) {
	c.migrateLegacyTitles()
	mux.HandleFunc("GET /api/device/health", c.authenticated(c.handleHealth))
	mux.HandleFunc("POST /api/device/messages", c.authenticated(c.handleCreateTurn))
	mux.HandleFunc("GET /api/device/turns/{id}", c.authenticated(c.handleGetTurn))
	mux.HandleFunc("GET /api/device/turns/{id}/audio", c.authenticated(c.handleTurnAudio))
	mux.HandleFunc("POST /api/device/turns/{id}/cancel", c.authenticated(c.handleCancelTurn))
	mux.HandleFunc("POST /api/device/sessions", c.authenticated(c.handleNewSession))
}

// Validate reports the configuration required to serve device turns. It runs at
// startup so a half-configured device channel fails loudly instead of accepting
// audio it can never answer.
func (c *Channel) Validate() error {
	deps := c.deps()
	cfg := deps.ConfigMgr.Get()
	if !cfg.DeviceEnabled {
		return nil
	}
	if strings.TrimSpace(cfg.DeviceToken) == "" {
		return errors.New("DEVICE_TOKEN is required when DEVICE_ENABLED=true")
	}
	if strings.TrimSpace(cfg.OllamaModelSpeech) == "" {
		return errors.New("OLLAMA_MODEL_SPEECH is required when DEVICE_ENABLED=true: the device can only answer with audio")
	}
	if deps.Client == nil {
		return errors.New("ollama client is required for the device channel")
	}
	transcribe, _, speech := c.capabilities(cfg, deps)
	if !transcribe {
		return errors.New("no model can transcribe audio: set OLLAMA_MODEL_AUDIO or use a main model with audio support")
	}
	if speech == "" {
		return errors.New("no speech model configured for the device channel")
	}
	return nil
}

type healthResponse struct {
	OK                 bool   `json:"ok"`
	Error              string `json:"error,omitempty"`
	CanTranscribe      bool   `json:"can_transcribe"`
	CanSpeak           bool   `json:"can_speak"`
	TranscriptionModel string `json:"transcription_model"`
	SpeechModel        string `json:"speech_model"`
	SessionID          string `json:"session_id,omitempty"`
	DeviceID           string `json:"device_id,omitempty"`
}

func (c *Channel) handleHealth(w http.ResponseWriter, r *http.Request) {
	deps := c.deps()
	cfg := deps.ConfigMgr.Get()
	transcribe, transcriptionModel, speechModel := c.capabilities(cfg, deps)
	resp := healthResponse{
		CanTranscribe:      transcribe,
		CanSpeak:           speechModel != "",
		TranscriptionModel: transcriptionModel,
		SpeechModel:        speechModel,
	}
	resp.OK = resp.CanTranscribe && resp.CanSpeak
	if !resp.OK {
		resp.Error = "device channel is not ready: audio input and speech output are both required"
	}
	if id, err := deviceID(r); err == nil {
		resp.DeviceID = id
		if deps.SessionStore != nil {
			resp.SessionID = c.links(deps).Get(id)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// capabilities resolves the models a device turn depends on. Transcription is
// available only with a dedicated audio model or a main model that the probe
// snapshot confirms as audio-capable; unknown capability is not enough for a
// device that can only speak its input.
func (c *Channel) capabilities(cfg config.Config, deps Deps) (transcribe bool, transcriptionModel, speechModel string) {
	mainModel := config.ResolveModel(cfg, config.ModelRoleMain)
	transcriptionModel = config.ResolveModel(cfg, config.ModelRoleAudio)
	if strings.TrimSpace(transcriptionModel) == "" {
		transcriptionModel = mainModel
	}
	speechModel = config.ResolveModel(cfg, config.ModelRoleSpeech)
	if strings.TrimSpace(mainModel) == "" {
		return false, transcriptionModel, speechModel
	}
	dedicated := strings.TrimSpace(config.ResolveModel(cfg, config.ModelRoleAudio))
	if dedicated != "" {
		return true, transcriptionModel, speechModel
	}
	checker := cache.Checker(engine.SnapshotPath(deps.CachePath))
	if checker == nil {
		// No probe snapshot: the main model's audio support is unconfirmed, so
		// the device cannot rely on transcribing its input.
		return false, transcriptionModel, speechModel
	}
	transcribe = checker(mainModel, "audio")
	return transcribe, transcriptionModel, speechModel
}

// authenticated rejects requests when the channel is disabled and validates the
// shared device token with a constant-time comparison.
func (c *Channel) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := c.config()
		if !cfg.DeviceEnabled {
			writeAPIError(w, http.StatusNotFound, "device channel is disabled")
			return
		}
		if !tokenValid(cfg.DeviceToken, requestToken(r)) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="device"`)
			writeAPIError(w, http.StatusUnauthorized, "invalid device token")
			return
		}
		next(w, r)
	}
}

// tokenValid compares the presented token with the configured one. An empty
// configured token never authenticates; Validate refuses to start that way.
func tokenValid(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func requestToken(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-Device-Token")); token != "" {
		return token
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > len("Bearer ") && strings.EqualFold(auth[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}
	return ""
}

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// deviceID reads and validates the X-Device-Id header, the stable identity that
// maps a physical device to its conversation.
func deviceID(r *http.Request) (string, error) {
	id := strings.TrimSpace(r.Header.Get("X-Device-Id"))
	if id == "" {
		return "", errors.New("X-Device-Id header is required")
	}
	if !deviceIDPattern.MatchString(id) {
		return "", errors.New("X-Device-Id must be 1-64 characters: letters, digits, underscore or dash")
	}
	return id, nil
}

func (c *Channel) config() config.Config {
	return c.deps().ConfigMgr.Get()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
