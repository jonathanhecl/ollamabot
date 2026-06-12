package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/learning"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	mu               sync.RWMutex
	cfg              config.Config
	envPath          string
	client           *ollama.Client
	runner           *probe.Runner
	mediaro          *router.Router
	cachePath        string
	sessionStore     *sessions.Store
	memoryStore      *memory.Store
	autoMgr          *agent.AutonomousManager
	goalMgr          *agent.GoalManager
	approvalsMu      sync.Mutex
	approvals        map[string]chan bool
	clarificationsMu sync.Mutex
	clarifications   map[string]chan string
	sleepMgr         *learning.SleepManager
}

type ModelView struct {
	Name             string                         `json:"name"`
	Family           string                         `json:"family"`
	Parameters       string                         `json:"parameters"`
	Quantization     string                         `json:"quantization"`
	ContextLength    int64                          `json:"context_length"`
	Capabilities     map[string]capabilities.Status `json:"capabilities"`
	CapabilityText   string                         `json:"capability_text"`
	Size             int64                          `json:"size"`
	SizeVRAM         int64                          `json:"size_vram"`
	Loaded           bool                           `json:"loaded"`
	ExpiresAt        string                         `json:"expires_at"`
	HasAudioEncoder  bool                           `json:"has_audio_encoder"`
	HasVisionEncoder bool                           `json:"has_vision_encoder"`
	IsDefault        bool                           `json:"is_default"`
	Source           string                         `json:"source"`
}

type ModelsResponse struct {
	BaseURL       string      `json:"base_url"`
	OllamaVersion string      `json:"ollama_version"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Models        []ModelView `json:"models"`
	FromCache     bool        `json:"from_cache"`
}

type SettingsResponse struct {
	OllamaBaseURL                string `json:"ollama_base_url"`
	ServerPort                   string `json:"server_port"`
	ServerEnabled                bool   `json:"server_enabled"`
	WebSearchEnabled             bool   `json:"web_search_enabled"`
	ServerExposeNetwork          bool   `json:"server_expose_network"`
	SessionAutoName              bool   `json:"session_auto_name"`
	ModelDefault                 string `json:"model_default"`
	ModelVision                  string `json:"model_vision"`
	ModelAudio                   string `json:"model_audio"`
	ModelEmbeddings              string `json:"model_embeddings"`
	ModelImage                   string `json:"model_image"`
	ImageSteps                   int    `json:"image_steps"`
	Workspace                    string `json:"workspace"`
	SessionsPath                 string `json:"sessions_path"`
	MemoryPath                   string `json:"memory_path"`
	SkillsPath                   string `json:"skills_path"`
	SleepModeEnabled             bool   `json:"sleep_mode_enabled"`
	SleepModeInactivityThreshold string `json:"sleep_mode_inactivity_threshold"`
	SleepModeResumeDelay         string `json:"sleep_mode_resume_delay"`
	ModelLearning                string `json:"model_learning"`
	SearchProviders              string `json:"search_providers"`      // comma-separated: "brave,ddg"
	BraveSearchAPIKey            string `json:"brave_search_api_key"`  // masked ("***") on GET if set
	TavilySearchAPIKey           string `json:"tavily_search_api_key"` // masked ("***") on GET if set
	SleepModeSubagentsEnabled    bool   `json:"sleep_mode_subagents_enabled"`
	ModelSubagent                string `json:"model_subagent"`
	ServerPassword               string `json:"server_password"`
	TelegramSessionExpiryMin     int    `json:"telegram_session_expiry_min"`
	TelegramBotToken             string `json:"telegram_bot_token"`      // masked ("***") on GET if set
	TelegramAuthorizedIDs        string `json:"telegram_authorized_ids"` // comma-separated
	TelegramStartupNotification  bool   `json:"telegram_startup_notification"`
}

// MediaMessage extends ollama.Message with per-image kind metadata sent by the
// frontend. ImageKinds[i] is "image" or "audio" for Images[i].
type MediaMessage = router.MediaMessage

type ChatRequest struct {
	Model     string         `json:"model"`
	Messages  []MediaMessage `json:"messages"`
	Think     bool           `json:"think"`
	SessionID string         `json:"session_id,omitempty"`
}

func NewServer(cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string) *Server {
	return NewServerWithEnv(cfg, client, runner, cachePath, ".env")
}

func NewServerWithEnv(cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string, envPath string) *Server {
	mr := router.New(client, routerConfig(cfg))
	ss := sessions.NewStore(cfg.SessionsPath)
	ms := memory.NewStore(cfg.MemoryPath)
	am := agent.NewAutonomousManager(cfg, client, ms)
	gm := agent.NewGoalManager(cfg, client)
	return &Server{
		cfg:            cfg,
		envPath:        envPath,
		client:         client,
		runner:         runner,
		mediaro:        mr,
		cachePath:      cachePath,
		sessionStore:   ss,
		memoryStore:    ms,
		autoMgr:        am,
		goalMgr:        gm,
		approvals:      make(map[string]chan bool),
		clarifications: make(map[string]chan string),
	}
}

func (s *Server) SetSleepManager(sm *learning.SleepManager) {
	s.mu.Lock()
	s.sleepMgr = sm
	s.mu.Unlock()
}

func (s *Server) SetGoalManager(gm *agent.GoalManager) {
	s.mu.Lock()
	s.goalMgr = gm
	s.mu.Unlock()
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("POST /api/models/reload", s.handleReloadModels)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("PUT /api/sessions/{id}", s.handleUpdateSession)
	mux.HandleFunc("POST /api/sessions/{id}/upload", s.handleSessionUpload)
	mux.HandleFunc("GET /api/sessions/{id}/uploads", s.handleListSessionUploads)
	mux.HandleFunc("GET /api/sessions/{id}/uploads/{filename}", s.handleDownloadSessionUpload)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/feedback", s.handleSessionFeedback)
	mux.HandleFunc("POST /api/sessions/{id}/goal", s.handleSessionGoal)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/memory", s.handleListMemory)
	mux.HandleFunc("POST /api/memory", s.handleAddMemory)
	mux.HandleFunc("POST /api/memory/search", s.handleSearchMemory)
	mux.HandleFunc("POST /api/memory/reindex", s.handleReindexMemory)
	mux.HandleFunc("DELETE /api/memory/{id}", s.handleDeleteMemory)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/tools/approve", s.handleApproveTool)
	mux.HandleFunc("POST /api/tools/clarify", s.handleClarifyTool)

	// Autonomous projects endpoints
	mux.HandleFunc("GET /api/autonomous/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/autonomous/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/autonomous/projects/{id}", s.handleGetProject)
	mux.HandleFunc("GET /api/autonomous/projects/{id}/logs/{logName}", s.handleGetProjectLog)
	mux.HandleFunc("POST /api/autonomous/projects/{id}/tick", s.handleTriggerProjectTick)
	mux.HandleFunc("POST /api/autonomous/projects/{id}/todos", s.handleAddProjectTodo)
	mux.HandleFunc("DELETE /api/autonomous/projects/{id}", s.handleDeleteProject)

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	cfg := s.config()
	port := strings.TrimPrefix(strings.TrimSpace(cfg.ServerPort), ":")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	if !cfg.ServerExposeNetwork {
		addr = "127.0.0.1" + addr
	}

	// Start background projects heartbeat ticker
	s.autoMgr.Start(context.Background())
	defer s.autoMgr.Stop()

	log.Printf("ollamabot web listening on %s", addr)
	return http.ListenAndServe(addr, s.authenticate(mux))
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		if cfg.ServerPassword == "" {
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			reqPass := r.Header.Get("X-Server-Password")
			if reqPass == "" {
				auth := r.Header.Get("Authorization")
				if strings.HasPrefix(auth, "Bearer ") {
					reqPass = strings.TrimPrefix(auth, "Bearer ")
				}
			}
			if reqPass == "" {
				reqPass = r.URL.Query().Get("password")
			}

			if reqPass != cfg.ServerPassword {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) deps() (config.Config, *ollama.Client, *probe.Runner, *router.Router) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.client, s.runner, s.mediaro
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, client, _, _ := s.deps()
	version, err := client.Version(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "ollama_version": version.Version})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	writeJSON(w, http.StatusOK, settingsResponse(cfg))
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var input SettingsResponse
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	baseURL, err := config.NormalizeBaseURL(input.OllamaBaseURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	workspace := strings.TrimSpace(input.Workspace)
	if workspace == "" {
		workspace = "workspace"
	}
	if !filepath.IsAbs(workspace) {
		if exe, err := os.Executable(); err == nil {
			workspace = filepath.Join(filepath.Dir(exe), workspace)
		}
	}
	_ = os.MkdirAll(workspace, 0o755)

	sessionsPath := strings.TrimSpace(input.SessionsPath)
	if sessionsPath == "" {
		sessionsPath = "sessions"
	}
	if !filepath.IsAbs(sessionsPath) {
		if exe, err := os.Executable(); err == nil {
			sessionsPath = filepath.Join(filepath.Dir(exe), sessionsPath)
		}
	}
	_ = os.MkdirAll(sessionsPath, 0o755)

	memoryPath := strings.TrimSpace(input.MemoryPath)
	if memoryPath == "" {
		memoryPath = "memory"
	}
	if !filepath.IsAbs(memoryPath) {
		if exe, err := os.Executable(); err == nil {
			memoryPath = filepath.Join(filepath.Dir(exe), memoryPath)
		}
	}
	_ = os.MkdirAll(memoryPath, 0o755)

	skillsPath := strings.TrimSpace(input.SkillsPath)
	if skillsPath == "" {
		skillsPath = "skills"
	}
	if !filepath.IsAbs(skillsPath) {
		if exe, err := os.Executable(); err == nil {
			skillsPath = filepath.Join(filepath.Dir(exe), skillsPath)
		}
	}
	_ = os.MkdirAll(skillsPath, 0o755)

	s.mu.Lock()
	s.cfg.OllamaBaseURL = baseURL
	s.cfg.ServerPort = strings.TrimPrefix(strings.TrimSpace(input.ServerPort), ":")
	if s.cfg.ServerPort == "" {
		s.cfg.ServerPort = "8080"
	}
	s.cfg.OllamaDefaultModel = strings.TrimSpace(input.ModelDefault)
	s.cfg.OllamaModelVision = strings.TrimSpace(input.ModelVision)
	s.cfg.OllamaModelAudio = strings.TrimSpace(input.ModelAudio)
	s.cfg.OllamaModelEmbed = strings.TrimSpace(input.ModelEmbeddings)
	s.cfg.OllamaModelImage = strings.TrimSpace(input.ModelImage)
	s.cfg.OllamaImageSteps = input.ImageSteps
	if s.cfg.OllamaImageSteps <= 0 {
		s.cfg.OllamaImageSteps = 4
	}
	s.cfg.WebSearchEnabled = input.WebSearchEnabled
	s.cfg.ServerExposeNetwork = input.ServerExposeNetwork
	s.cfg.SessionAutoName = input.SessionAutoName
	s.cfg.TelegramSessionExpiryMin = input.TelegramSessionExpiryMin
	if s.cfg.TelegramSessionExpiryMin <= 0 {
		s.cfg.TelegramSessionExpiryMin = 30
	}
	s.cfg.TelegramStartupNotification = input.TelegramStartupNotification
	// Telegram Bot Token: only update if not masked sentinel
	newBotToken := strings.TrimSpace(input.TelegramBotToken)
	if newBotToken != "" && newBotToken != "***" {
		s.cfg.TelegramBotToken = newBotToken
	} else if newBotToken == "" {
		s.cfg.TelegramBotToken = ""
	}
	// Telegram Authorized IDs: parse CSV
	rawAuthorizedIDs := strings.TrimSpace(input.TelegramAuthorizedIDs)
	if rawAuthorizedIDs != "" {
		var ids []string
		for _, id := range strings.Split(rawAuthorizedIDs, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				ids = append(ids, id)
			}
		}
		s.cfg.TelegramAuthorizedIDs = ids
	} else {
		s.cfg.TelegramAuthorizedIDs = []string{}
	}
	s.cfg.Workspace = workspace
	s.cfg.SessionsPath = sessionsPath
	s.cfg.MemoryPath = memoryPath
	s.cfg.SkillsPath = skillsPath
	s.cfg.SleepModeEnabled = input.SleepModeEnabled
	s.cfg.SleepModeInactivityThreshold = strings.TrimSpace(input.SleepModeInactivityThreshold)
	s.cfg.SleepModeResumeDelay = strings.TrimSpace(input.SleepModeResumeDelay)
	s.cfg.OllamaModelLearning = strings.TrimSpace(input.ModelLearning)
	s.cfg.SleepModeSubagentsEnabled = input.SleepModeSubagentsEnabled
	s.cfg.OllamaModelSubagent = strings.TrimSpace(input.ModelSubagent)
	newServerPass := strings.TrimSpace(input.ServerPassword)
	if newServerPass != "" && newServerPass != "***" {
		s.cfg.ServerPassword = newServerPass
	} else if newServerPass == "" {
		s.cfg.ServerPassword = ""
	}
	// Search providers: parse CSV from UI
	rawProviders := strings.TrimSpace(input.SearchProviders)
	if rawProviders != "" && rawProviders != "none" {
		var ps []string
		for _, p := range strings.Split(rawProviders, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				ps = append(ps, p)
			}
		}
		s.cfg.SearchProviders = ps
	} else if rawProviders == "none" || rawProviders == "" {
		s.cfg.SearchProviders = []string{"none"}
	}
	// Helper to check if a provider is active in the updated configuration
	isProviderActive := func(provider string) bool {
		if !input.WebSearchEnabled {
			return false
		}
		for _, p := range s.cfg.SearchProviders {
			if p == provider {
				return true
			}
		}
		return false
	}

	// Brave API key: only update if not masked sentinel.
	// Only clear if the provider is active; if inactive, keep the existing key.
	newKey := strings.TrimSpace(input.BraveSearchAPIKey)
	if newKey != "" && newKey != "***" {
		s.cfg.BraveSearchAPIKey = newKey
	} else if newKey == "" && isProviderActive("brave") {
		s.cfg.BraveSearchAPIKey = ""
	}

	// Tavily API key: only update if not masked sentinel.
	// Only clear if the provider is active; if inactive, keep the existing key.
	newTavilyKey := strings.TrimSpace(input.TavilySearchAPIKey)
	if newTavilyKey != "" && newTavilyKey != "***" {
		s.cfg.TavilyAPIKey = newTavilyKey
	} else if newTavilyKey == "" && isProviderActive("tavily") {
		s.cfg.TavilyAPIKey = ""
	}
	// Update WebSearchEnabled based on providers
	s.cfg.WebSearchEnabled = len(s.cfg.SearchProviders) > 0 &&
		!(len(s.cfg.SearchProviders) == 1 && s.cfg.SearchProviders[0] == "none")
	s.client = ollama.NewClient(baseURL)
	s.runner = probe.NewRunner(s.client)
	s.mediaro = router.New(s.client, routerConfig(s.cfg))
	s.sessionStore = sessions.NewStore(sessionsPath)
	s.memoryStore = memory.NewStore(memoryPath)
	if s.autoMgr != nil {
		s.autoMgr.Stop()
	}
	s.autoMgr = agent.NewAutonomousManager(s.cfg, s.client, s.memoryStore)
	s.autoMgr.Start(context.Background())
	if s.sleepMgr != nil {
		s.sleepMgr.Pause()
	}
	if s.cfg.SleepModeEnabled {
		s.sleepMgr = learning.NewSleepManager(s.cfg, s.client)
		s.sleepMgr.Start(context.Background())
	} else {
		s.sleepMgr = nil
	}
	cfg := s.cfg
	s.mu.Unlock()

	if strings.TrimSpace(s.envPath) != "" {
		_ = config.SaveBasic(s.envPath, cfg)
	}
	writeJSON(w, http.StatusOK, settingsResponse(cfg))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	response, err := s.models(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleReloadModels(w http.ResponseWriter, r *http.Request) {
	cfg, client, runner, _ := s.deps()
	version, err := client.Version(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	reports, err := runner.Inventory(r.Context(), cfg.OllamaProbeModels)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	ps, _ := client.Ps(r.Context())

	// Read existing snapshot first to preserve expected probes & probe runs
	path := s.cachePath
	if path == "" {
		path = SnapshotPath("")
	}

	var oldSnapshot cache.Snapshot
	if path != "" {
		if loaded, err := cache.Load(path); err == nil {
			oldSnapshot = loaded
		}
	}

	snapshot := cache.Snapshot{
		GeneratedAt:   time.Now(),
		BaseURL:       cfg.OllamaBaseURL,
		OllamaVersion: version.Version,
		Models:        reports,
		Running:       ps.Models,
		Expected:      oldSnapshot.Expected,
		ProbeRuns:     oldSnapshot.ProbeRuns,
	}

	if len(snapshot.Expected) == 0 {
		snapshot.Expected = []cache.ExpectedProbe{
			{Name: "models", Status: capabilities.Confirmed, Details: "Inventory from /api/tags and /api/show"},
			{Name: "audio", Status: capabilities.Pending, Details: "Audio remains pending unless an end-to-end REST payload is confirmed"},
			{Name: "video", Status: capabilities.Pending, Details: "Video remains pending; planned path is frame extraction plus vision"},
		}
	}

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = cache.Save(path, snapshot)
		}
	}

	response, err := s.models(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func routerConfig(cfg config.Config) router.Config {
	return router.Config{
		MainModel:   cfg.OllamaDefaultModel,
		VisionModel: cfg.OllamaModelVision,
		AudioModel:  cfg.OllamaModelAudio,
		ImageModel:  cfg.OllamaModelImage,
		ImageSteps:  cfg.OllamaImageSteps,
	}
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	sm := s.sleepMgr
	s.mu.RUnlock()
	if sm != nil {
		sm.NotifyUserActivity()
	}

	var input ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" {
		writeError(w, http.StatusBadRequest, errors.New("model is required"))
		return
	}
	if len(input.Messages) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("messages are required"))
		return
	}

	cfg, client, _, _ := s.deps()

	// Summarise the incoming request
	lastMsg := input.Messages[len(input.Messages)-1]
	imageCount := len(lastMsg.Images)
	log.Printf("[Web] Chat request model=%q think=%v text_len=%d messages=%d images=%d",
		input.Model, input.Think, len(lastMsg.Content), len(input.Messages), imageCount)

	// Build a per-request media router: the main model is the one selected by
	// the frontend (which may differ from the configured default), and routing
	// decisions are based on real probed capabilities when available.
	rcfg := routerConfig(cfg)
	rcfg.MainModel = input.Model
	rcfg.HasCapability = cache.Checker(SnapshotPath(s.cachePath))
	mr := router.New(client, rcfg)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// Pre-process media attachments using role models before sending to main.
	ollamaMessages, err := resolveMedia(r.Context(), mr, input.Messages, w, flusher)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	registry := tools.NewRegistry(cfg.WebSearchEnabled, cfg.Workspace, s.memoryStore, client, cfg.OllamaModelEmbed, tools.SearchConfig{
		Providers:    cfg.SearchProviders,
		BraveAPIKey:  cfg.BraveSearchAPIKey,
		TavilyAPIKey: cfg.TavilyAPIKey,
	})
	registry.SetApprovalHandler(&webApprovalHandler{
		server:  s,
		w:       w,
		flusher: flusher,
	})
	registry.SetClarificationHandler(&webClarificationHandler{
		server:  s,
		w:       w,
		flusher: flusher,
	})
	registry.SetImageProgressHandler(&webImageProgressHandler{
		w:       w,
		flusher: flusher,
	})

	// Inject uploaded-files context if session has uploads
	if input.SessionID != "" {
		ollamaMessages = injectUploadsContext(cfg.Workspace, cfg.SessionsPath, input.SessionID, ollamaMessages)
	}

	log.Printf("[Web] Running agent model=%q think=%v messages=%d", input.Model, input.Think, len(ollamaMessages))
	err = runChatStream(r.Context(), cfg, client, input.Model, ollamaMessages, input.Think, registry, w, flusher)
	if err != nil {
		log.Printf("[Web] Agent error: %v", err)
		writeSSE(w, "error", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
	} else {
		log.Printf("[Web] Agent completed model=%q", input.Model)
	}
}

type sseStreamHandler struct {
	w       http.ResponseWriter
	flusher http.Flusher
	model   string
}

func (h *sseStreamHandler) OnThinking(delta string) {
	writeSSE(h.w, "thinking", delta)
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnContent(delta string) {
	writeSSE(h.w, "content", delta)
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnToolCall(call ollama.ToolCall) {
	writeSSE(h.w, "tool_call", call)
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnToolStart(name string, args any) {
	log.Printf("[Web] Tool start: %s", name)
	writeSSE(h.w, "tool_start", map[string]any{"name": name, "arguments": args})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnToolResult(name string, result string) {
	log.Printf("[Web] Tool result: %s (len=%d)", name, len(result))
	writeSSE(h.w, "tool_result", map[string]any{"name": name, "result": result})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnMediaPreProcessing(content string) {
	log.Printf("[Web] Media pre-processing (len=%d)", len(content))
	writeSSE(h.w, "media_pre_processing", content)
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnDone(resp ollama.ChatResponse) {
	if resp.TotalDuration > 0 {
		tokensPerSec := 0.0
		if resp.EvalDuration > 0 {
			tokensPerSec = float64(resp.EvalCount) / (float64(resp.EvalDuration) / 1e9)
		}
		log.Printf("[Web] Done model=%q total=%.2fs eval=%d tokens (%.1f t/s) prompt=%d tokens",
			h.model,
			float64(resp.TotalDuration)/1e9,
			resp.EvalCount, tokensPerSec,
			resp.PromptEvalCount,
		)
	}
	writeSSE(h.w, "done", map[string]any{
		"total_duration":       resp.TotalDuration,
		"load_duration":        resp.LoadDuration,
		"prompt_eval_count":    resp.PromptEvalCount,
		"prompt_eval_duration": resp.PromptEvalDuration,
		"eval_count":           resp.EvalCount,
		"eval_duration":        resp.EvalDuration,
	})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

// runChatStream handles the chat streaming loop by delegating to the iterative agent.
func runChatStream(ctx context.Context, cfg config.Config, client *ollama.Client, model string, messages []ollama.Message, think bool, registry *tools.Registry, w http.ResponseWriter, flusher http.Flusher) error {
	a := agent.NewAgent(cfg, client, registry)
	handler := &sseStreamHandler{w: w, flusher: flusher, model: model}

	_, err := a.Run(ctx, model, messages, think, handler)
	if err != nil {
		return err
	}

	// Send final done chunk to signal completion to frontend
	writeSSE(w, "done", map[string]any{
		"model":  model,
		"reason": "stop",
	})
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// resolveMedia pre-processes the latest user message's attachments with the
// shared media pipeline (router.ResolveMessages) and streams the structured
// per-attachment results to the frontend as a "media_pre_processing" event.
func resolveMedia(ctx context.Context, mr *router.Router, messages []MediaMessage, w http.ResponseWriter, flusher http.Flusher) ([]ollama.Message, error) {
	res, err := mr.ResolveMessages(ctx, messages)
	if err != nil {
		return nil, err
	}
	if len(res.Attachments) > 0 && w != nil {
		writeSSE(w, "media_pre_processing", map[string]any{
			"summary":     res.ContextNote,
			"attachments": res.Attachments,
		})
		if flusher != nil {
			flusher.Flush()
		}
	}
	return res.Messages, nil
}

func (s *Server) models(ctx context.Context) (ModelsResponse, error) {
	cfg, client, runner, _ := s.deps()
	version, _ := client.Version(ctx)
	reports, err := runner.Inventory(ctx, cfg.OllamaProbeModels)
	if err != nil {
		if cached, cacheErr := cache.Load(s.cachePath); cacheErr == nil {
			return s.modelsFromCache(cached), nil
		}
		return ModelsResponse{}, err
	}

	ps, _ := client.Ps(ctx)
	running := runningByName(ps.Models)
	views := make([]ModelView, 0, len(reports))
	for _, report := range reports {
		view := modelView(report, nil, cfg.OllamaDefaultModel, "live")
		if current, ok := running[report.Name]; ok {
			view = modelView(report, &current, cfg.OllamaDefaultModel, "live")
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Loaded != views[j].Loaded {
			return views[i].Loaded
		}
		return views[i].Name < views[j].Name
	})
	return ModelsResponse{
		BaseURL:       cfg.OllamaBaseURL,
		OllamaVersion: version.Version,
		GeneratedAt:   time.Now(),
		Models:        views,
	}, nil
}

func (s *Server) modelsFromCache(snapshot cache.Snapshot) ModelsResponse {
	cfg := s.config()
	running := runningByName(snapshot.Running)
	views := make([]ModelView, 0, len(snapshot.Models))
	for _, report := range snapshot.Models {
		view := modelView(report, nil, cfg.OllamaDefaultModel, "cache")
		if current, ok := running[report.Name]; ok {
			view = modelView(report, &current, cfg.OllamaDefaultModel, "cache")
		}
		views = append(views, view)
	}
	return ModelsResponse{
		BaseURL:       snapshot.BaseURL,
		OllamaVersion: snapshot.OllamaVersion,
		GeneratedAt:   snapshot.GeneratedAt,
		Models:        views,
		FromCache:     true,
	}
}

func runningByName(models []ollama.RunningModel) map[string]ollama.RunningModel {
	out := map[string]ollama.RunningModel{}
	for _, model := range models {
		out[model.Name] = model
		out[model.Model] = model
	}
	return out
}

func modelView(report capabilities.ModelReport, running *ollama.RunningModel, defaultModel string, source string) ModelView {
	view := ModelView{
		Name:             report.Name,
		Family:           report.Family,
		Parameters:       report.Parameters,
		Quantization:     report.Quantization,
		ContextLength:    report.ContextLength,
		Capabilities:     report.Capabilities,
		CapabilityText:   capabilities.StatusList(report.Capabilities),
		HasAudioEncoder:  report.HasAudioEncoder,
		HasVisionEncoder: report.HasVisionEncoder,
		IsDefault:        report.Name == defaultModel,
		Source:           source,
	}
	if running != nil {
		view.Loaded = true
		view.Size = running.Size
		view.SizeVRAM = running.SizeVRAM
		view.ExpiresAt = running.ExpiresAt
		if running.ContextLength > 0 {
			view.ContextLength = running.ContextLength
		}
	}
	return view
}

type createSessionRequest struct {
	Title string `json:"title"`
	Model string `json:"model"`
}

type updateSessionRequest struct {
	Title    string            `json:"title,omitempty"`
	Model    string            `json:"model,omitempty"`
	Messages []json.RawMessage `json:"messages,omitempty"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.sessionStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sess := sessions.Session{
		ID:    sessions.GenerateID(),
		Title: strings.TrimSpace(req.Title),
		Model: strings.TrimSpace(req.Model),
	}
	if sess.Title == "" {
		sess.Title = "New session"
	}
	if err := s.sessionStore.Save(sess); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessions.NotifyUpdate(sess.ID)
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.sessionStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sess, err := s.sessionStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if req.Title != "" {
		sess.Title = req.Title
	}
	if req.Model != "" {
		sess.Model = req.Model
	}
	if req.Messages != nil {
		sess.Messages = req.Messages
	}
	if err := s.sessionStore.Save(sess); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessions.NotifyUpdate(sess.ID)
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sessionStore.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessions.NotifyUpdate(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// uploadsDir returns the path to the per-session uploads directory.
// Sessions are stored directly under sessionsPath, so uploads live at
// sessionsPath/sessionID/uploads (workspace is not part of this path).
func uploadsDir(workspace, sessionsPath, sessionID string) string {
	_ = workspace // kept for signature compatibility; not used in path construction
	return filepath.Join(sessionsPath, sessionID, "uploads")
}

// sanitizeUploadName returns a safe filename by stripping path separators and
// limiting length. The original extension is preserved.
func sanitizeUploadName(raw string) string {
	base := filepath.Base(raw)
	if base == "." || base == "" {
		base = "file"
	}
	// Replace any remaining path separator chars.
	base = strings.ReplaceAll(base, "/", "_")
	base = strings.ReplaceAll(base, "\\", "_")
	if len(base) > 200 {
		ext := filepath.Ext(base)
		base = base[:200-len(ext)] + ext
	}
	return base
}

// handleSessionUpload accepts a multipart file upload and saves it to the
// session's uploads directory within the workspace.
func (s *Server) handleSessionUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg, _, _, _ := s.deps()

	// 64 MiB max upload
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid multipart: %w", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
		return
	}
	defer file.Close()

	dir := uploadsDir(cfg.Workspace, cfg.SessionsPath, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	name := sanitizeUploadName(header.Filename)
	// Avoid collisions by appending a timestamp if file already exists.
	destPath := filepath.Join(dir, name)
	if _, statErr := os.Stat(destPath); statErr == nil {
		ext := filepath.Ext(name)
		noExt := strings.TrimSuffix(name, ext)
		name = fmt.Sprintf("%s_%d%s", noExt, time.Now().UnixMilli(), ext)
		destPath = filepath.Join(dir, name)
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = detectMime(name)
	}
	relPath := filepath.Join("sessions", id, "uploads", name)

	log.Printf("[Web] Upload session=%s file=%q mime=%s size=%d", id, name, mime, len(data))
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name,
		"path": relPath,
		"mime": mime,
		"size": len(data),
	})
}

// handleListSessionUploads returns a list of files in the session uploads dir.
func (s *Server) handleListSessionUploads(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg, _, _, _ := s.deps()

	dir := uploadsDir(cfg.Workspace, cfg.SessionsPath, id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []any{})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type fileInfo struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Mime string `json:"mime"`
		Size int64  `json:"size"`
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		files = append(files, fileInfo{
			Name: name,
			Path: filepath.Join("sessions", id, "uploads", name),
			Mime: detectMime(name),
			Size: info.Size(),
		})
	}
	if files == nil {
		files = []fileInfo{}
	}
	writeJSON(w, http.StatusOK, files)
}

// handleDownloadSessionUpload serves a single uploaded file for download.
func (s *Server) handleDownloadSessionUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	filename := r.PathValue("filename")
	cfg, _, _, _ := s.deps()

	// Sanitize: only allow bare filenames with no path traversal
	clean := filepath.Base(filename)
	if clean == "." || clean == "" || strings.Contains(clean, "..") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid filename"))
		return
	}

	filePath := filepath.Join(uploadsDir(cfg.Workspace, cfg.SessionsPath, id), clean)
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()

	mime := detectMime(clean)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+clean+`"`)
	http.ServeContent(w, r, clean, time.Time{}, f)
}

func humanSize(b int64) string {
	const kb, mb = 1024, 1024 * 1024
	switch {
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/mb)
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/kb)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func detectMime(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md", ".csv", ".log":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "application/xml"
	case ".zip":
		return "application/zip"
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".py":
		return "text/x-python"
	case ".go":
		return "text/x-go"
	case ".js":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".rs":
		return "text/x-rust"
	case ".c", ".h":
		return "text/x-c"
	case ".cpp", ".hpp":
		return "text/x-c++"
	case ".java":
		return "text/x-java"
	case ".sh", ".bash":
		return "text/x-shellscript"
	default:
		return "application/octet-stream"
	}
}

// injectUploadsContext prepends a system message listing uploaded files if the
// session has any, so the agent knows what files are available.
func injectUploadsContext(workspace, sessionsPath, sessionID string, messages []ollama.Message) []ollama.Message {
	dir := uploadsDir(workspace, sessionsPath, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return messages
	}

	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Build the path the agent should use with read_file (relative to workspace).
		// Use filepath.Rel so it works regardless of whether sessionsPath is absolute or relative.
		absFile := filepath.Join(dir, e.Name())
		relPath, relErr := filepath.Rel(workspace, absFile)
		if relErr != nil {
			relPath = absFile // fall back to absolute if Rel fails
		}
		// Always use forward slashes (read_file expects POSIX-style separators).
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		sizeStr := ""
		if info, err := e.Info(); err == nil {
			sizeStr = fmt.Sprintf(" (%s)", humanSize(info.Size()))
		}
		lines = append(lines, fmt.Sprintf("- %s%s  (path: %s)", e.Name(), sizeStr, relPath))
	}
	if len(lines) == 0 {
		return messages
	}
	note := "The user has uploaded the following files to this session. " +
		"You can read text files with the read_file tool using the given path, " +
		"or run shell commands on binary/video files with execute_command.\n\nUploaded files:\n" +
		strings.Join(lines, "\n")

	// Find existing system message and append to it, or prepend a new one.
	for i, msg := range messages {
		if msg.Role == "system" {
			messages[i].Content = messages[i].Content + "\n\n" + note
			return messages
		}
	}
	sys := ollama.Message{Role: "system", Content: note}
	return append([]ollama.Message{sys}, messages...)
}

func (s *Server) handleSessionFeedback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input struct {
		MessageIndex int    `json:"message_index"`
		Reaction     string `json:"reaction"` // "positive" or "negative"
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.Reaction != "positive" && input.Reaction != "negative" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("reaction must be 'positive' or 'negative'"))
		return
	}

	fb := sessions.Feedback{
		MessageIndex: input.MessageIndex,
		Reaction:     input.Reaction,
		Timestamp:    time.Now(),
	}
	if err := s.sessionStore.SaveFeedback(id, fb); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessions.NotifyUpdate(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type addMemoryRequest struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

type searchMemoryRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
}

func (s *Server) handleListMemory(w http.ResponseWriter, r *http.Request) {
	entries := s.memoryStore.List()
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "count": len(entries)})
}

func (s *Server) handleAddMemory(w http.ResponseWriter, r *http.Request) {
	var req addMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	cfg := s.config()
	embedModel := cfg.OllamaModelEmbed
	if embedModel == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("no embedding model configured"))
		return
	}
	resp, err := s.client.Embed(r.Context(), ollama.EmbedRequest{Model: embedModel, Input: req.Text})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(resp.Embeddings) == 0 {
		writeError(w, http.StatusInternalServerError, errors.New("empty embedding response"))
		return
	}
	entry := memory.Entry{Text: req.Text, Source: req.Source, Embedding: resp.Embeddings[0]}
	if err := s.memoryStore.Add(&entry); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "added", "id": entry.ID})
}

func (s *Server) handleSearchMemory(w http.ResponseWriter, r *http.Request) {
	var req searchMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, errors.New("query is required"))
		return
	}
	cfg := s.config()
	embedModel := cfg.OllamaModelEmbed
	if embedModel == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("no embedding model configured"))
		return
	}
	resp, err := s.client.Embed(r.Context(), ollama.EmbedRequest{Model: embedModel, Input: req.Query})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(resp.Embeddings) == 0 {
		writeError(w, http.StatusInternalServerError, errors.New("empty embedding response"))
		return
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 3
	}
	results := s.memoryStore.Search(resp.Embeddings[0], topK)
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.memoryStore.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleReindexMemory(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	embedModel := cfg.OllamaModelEmbed
	if embedModel == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("no embedding model configured"))
		return
	}

	entries := s.memoryStore.List()
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"count":  0,
			"model":  embedModel,
		})
		return
	}

	// Fetch embeddings sequentially to avoid overloading local Ollama/GPU
	newEmbeddings := make(map[string][]float64)
	for _, entry := range entries {
		resp, err := s.client.Embed(r.Context(), ollama.EmbedRequest{Model: embedModel, Input: entry.Text})
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("failed embedding entry '%s': %w", entry.ID, err))
			return
		}
		if len(resp.Embeddings) == 0 {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("empty embedding response for entry '%s'", entry.ID))
			return
		}
		newEmbeddings[entry.ID] = resp.Embeddings[0]
	}

	if err := s.memoryStore.UpdateEmbeddings(newEmbeddings); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed updating embeddings: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"count":  len(newEmbeddings),
		"model":  embedModel,
	})
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	payload, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: %s\n", event)
	fmt.Fprintf(w, "data: %s\n\n", payload)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func settingsResponse(cfg config.Config) SettingsResponse {
	// Mask the Brave API key to avoid leaking it through the API.
	maskedKey := ""
	if cfg.BraveSearchAPIKey != "" {
		maskedKey = "***"
	}
	maskedTavily := ""
	if cfg.TavilyAPIKey != "" {
		maskedTavily = "***"
	}
	maskedServerPassword := ""
	if cfg.ServerPassword != "" {
		maskedServerPassword = "***"
	}
	maskedTelegramBotToken := ""
	if cfg.TelegramBotToken != "" {
		maskedTelegramBotToken = "***"
	}
	return SettingsResponse{
		OllamaBaseURL:                cfg.OllamaBaseURL,
		ServerPort:                   cfg.ServerPort,
		ServerEnabled:                cfg.ServerEnabled,
		WebSearchEnabled:             cfg.WebSearchEnabled,
		ServerExposeNetwork:          cfg.ServerExposeNetwork,
		SessionAutoName:              cfg.SessionAutoName,
		TelegramSessionExpiryMin:     cfg.TelegramSessionExpiryMin,
		ModelDefault:                 cfg.OllamaDefaultModel,
		ModelVision:                  cfg.OllamaModelVision,
		ModelAudio:                   cfg.OllamaModelAudio,
		ModelEmbeddings:              cfg.OllamaModelEmbed,
		ModelImage:                   cfg.OllamaModelImage,
		ImageSteps:                   cfg.OllamaImageSteps,
		Workspace:                    cfg.Workspace,
		SessionsPath:                 cfg.SessionsPath,
		MemoryPath:                   cfg.MemoryPath,
		SkillsPath:                   cfg.SkillsPath,
		SleepModeEnabled:             cfg.SleepModeEnabled,
		SleepModeInactivityThreshold: cfg.SleepModeInactivityThreshold,
		SleepModeResumeDelay:         cfg.SleepModeResumeDelay,
		ModelLearning:                cfg.OllamaModelLearning,
		SearchProviders:              strings.Join(cfg.SearchProviders, ","),
		BraveSearchAPIKey:            maskedKey,
		TavilySearchAPIKey:           maskedTavily,
		SleepModeSubagentsEnabled:    cfg.SleepModeSubagentsEnabled,
		ModelSubagent:                cfg.OllamaModelSubagent,
		ServerPassword:               maskedServerPassword,
		TelegramBotToken:             maskedTelegramBotToken,
		TelegramAuthorizedIDs:        strings.Join(cfg.TelegramAuthorizedIDs, ","),
		TelegramStartupNotification:  cfg.TelegramStartupNotification,
	}
}

func SnapshotPath(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	if _, err := os.Stat("docs"); err == nil {
		return "docs/probe-cache.json"
	}
	return "probe-cache.json"
}

// truncate returns s truncated to maxLen characters with "..." appended if needed.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.autoMgr.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createProjectRequest struct {
	Name string `json:"name"`
	Goal string `json:"goal"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Name == "" || req.Goal == "" {
		writeError(w, http.StatusBadRequest, errors.New("name and goal are required"))
		return
	}
	proj, err := s.autoMgr.CreateProject(r.Context(), req.Name, req.Goal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, proj)
}

type getProjectResponse struct {
	Project agent.Project `json:"project"`
	Logs    []string      `json:"logs"`
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, err := s.autoMgr.LoadProject(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	logs, _ := s.autoMgr.GetProjectLogs(id)
	writeJSON(w, http.StatusOK, getProjectResponse{
		Project: proj,
		Logs:    logs,
	})
}

func (s *Server) handleGetProjectLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	logName := r.PathValue("logName")
	logName = filepath.Clean(logName)
	if strings.Contains(logName, "..") || filepath.IsAbs(logName) {
		writeError(w, http.StatusBadRequest, errors.New("invalid log filename"))
		return
	}
	cfg := s.config()
	logPath := filepath.Join(cfg.Workspace, id, "logs", logName)
	content, err := os.ReadFile(logPath)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleTriggerProjectTick(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, err := s.autoMgr.LoadProject(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	taskIdx := -1
	for i, todo := range proj.Todos {
		if todo.Status == "pending" || todo.Status == "in_progress" {
			taskIdx = i
			break
		}
	}

	if taskIdx == -1 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "idle", "message": "All tasks are already completed"})
		return
	}

	err = s.autoMgr.ExecuteTask(r.Context(), id, taskIdx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	updatedProj, _ := s.autoMgr.LoadProject(id)
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "project": updatedProj})
}

type addProjectTodoRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleAddProjectTodo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req addProjectTodoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, errors.New("content is required"))
		return
	}
	proj, err := s.autoMgr.LoadProject(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	newID := fmt.Sprintf("task-%d", len(proj.Todos)+1)
	proj.Todos = append(proj.Todos, agent.ProjectTodo{
		ID:        newID,
		Content:   req.Content,
		Status:    "pending",
		UpdatedAt: time.Now(),
	})
	if proj.Status == "completed" {
		proj.Status = "pending"
	}
	if err := s.autoMgr.SaveProject(proj); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, proj)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.autoMgr.DeleteProject(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type approveToolRequest struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
}

func (s *Server) handleApproveTool(w http.ResponseWriter, r *http.Request) {
	var req approveToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing id"))
		return
	}

	s.approvalsMu.Lock()
	ch, ok := s.approvals[req.ID]
	s.approvalsMu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("approval request not found or expired"))
		return
	}

	// Notify the waiting block
	select {
	case ch <- req.Approved:
		writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
	default:
		writeError(w, http.StatusGone, fmt.Errorf("approval channel was closed or already answered"))
	}
}

type webApprovalHandler struct {
	server  *Server
	w       http.ResponseWriter
	flusher http.Flusher
}

func (h *webApprovalHandler) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	approvalID := fmt.Sprintf("web_%d_%s", time.Now().UnixNano(), toolName)
	ch := make(chan bool, 1) // buffered to avoid blocking the responder

	h.server.approvalsMu.Lock()
	h.server.approvals[approvalID] = ch
	h.server.approvalsMu.Unlock()

	defer func() {
		h.server.approvalsMu.Lock()
		delete(h.server.approvals, approvalID)
		h.server.approvalsMu.Unlock()
	}()

	// Send SSE approval request event
	writeSSE(h.w, "tool_approval_required", map[string]any{
		"id":        approvalID,
		"tool":      toolName,
		"arguments": args,
	})
	if h.flusher != nil {
		h.flusher.Flush()
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case approved := <-ch:
			return approved, nil
		case <-ticker.C:
			// Send ping comment to keep SSE stream open
			_, _ = h.w.Write([]byte(": ping\n\n"))
			if h.flusher != nil {
				h.flusher.Flush()
			}
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timeout:
			return false, fmt.Errorf("approval timeout")
		}
	}
}

type clarifyToolRequest struct {
	ID     string `json:"id"`
	Option string `json:"option"`
}

func (s *Server) handleClarifyTool(w http.ResponseWriter, r *http.Request) {
	var req clarifyToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Option = strings.TrimSpace(req.Option)
	if req.ID == "" || req.Option == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing id or option"))
		return
	}

	s.clarificationsMu.Lock()
	ch, ok := s.clarifications[req.ID]
	s.clarificationsMu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("clarification request not found or expired"))
		return
	}

	// Notify the waiting block
	select {
	case ch <- req.Option:
		writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
	default:
		writeError(w, http.StatusGone, fmt.Errorf("clarification channel was closed or already answered"))
	}
}

type webClarificationHandler struct {
	server  *Server
	w       http.ResponseWriter
	flusher http.Flusher
}

func (h *webClarificationHandler) RequestClarification(ctx context.Context, question string, options []string) (string, error) {
	clarifyID := fmt.Sprintf("web_clarify_%d", time.Now().UnixNano())
	ch := make(chan string, 1)

	h.server.clarificationsMu.Lock()
	h.server.clarifications[clarifyID] = ch
	h.server.clarificationsMu.Unlock()

	defer func() {
		h.server.clarificationsMu.Lock()
		delete(h.server.clarifications, clarifyID)
		h.server.clarificationsMu.Unlock()
	}()

	// Send SSE clarification request event
	writeSSE(h.w, "tool_clarification_required", map[string]any{
		"id":       clarifyID,
		"question": question,
		"options":  options,
	})
	if h.flusher != nil {
		h.flusher.Flush()
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case chosen := <-ch:
			return chosen, nil
		case <-ticker.C:
			// Send ping comment to keep SSE stream open
			_, _ = h.w.Write([]byte(": ping\n\n"))
			if h.flusher != nil {
				h.flusher.Flush()
			}
		case <-ctx.Done():
			chosen := selectDefaultOption(options)
			log.Printf("[Web] Clarification cancelled. Proceeding with default option: %q", chosen)
			return fmt.Sprintf("Clarification was cancelled or timed out. Proceeding with default option: %s", chosen), nil
		case <-timeout:
			chosen := selectDefaultOption(options)
			log.Printf("[Web] Clarification timed out. Auto-selected default option: %q", chosen)
			return chosen, nil
		}
	}
}

// webImageProgressHandler handles image generation progress updates for Web UI via SSE
type webImageProgressHandler struct {
	w       http.ResponseWriter
	flusher http.Flusher
	genID   string
}

func (h *webImageProgressHandler) SetGenerationID(id string) {
	h.genID = id
}

func (h *webImageProgressHandler) OnProgress(completed, total int, status string) {
	writeSSE(h.w, "image_progress", map[string]any{
		"completed": completed,
		"total":     total,
		"status":    status,
		"gen_id":    h.genID,
	})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *webImageProgressHandler) OnComplete(imagePath string) {
	writeSSE(h.w, "image_complete", map[string]any{
		"path":   imagePath,
		"gen_id": h.genID,
	})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func selectDefaultOption(options []string) string {
	if len(options) == 0 {
		return ""
	}
	for _, opt := range options {
		low := strings.ToLower(opt)
		if strings.Contains(low, "recommended") || strings.Contains(low, "recomendado") || strings.Contains(low, "default") || strings.Contains(low, "predeterminado") {
			return opt
		}
	}
	return options[0]
}

type sessionGoalRequest struct {
	Action    string `json:"action"` // "start", "pause", "resume", "clear"
	Objective string `json:"objective,omitempty"`
}

func (s *Server) handleSessionGoal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req sessionGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	s.mu.RLock()
	goalMgr := s.goalMgr
	s.mu.RUnlock()

	if goalMgr == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("GoalManager not initialized"))
		return
	}

	var err error
	switch req.Action {
	case "start":
		err = goalMgr.StartGoal(id, req.Objective)
	case "pause":
		err = goalMgr.PauseGoal(id)
	case "resume":
		err = goalMgr.ResumeGoal(id)
	case "clear":
		err = goalMgr.ClearGoal(id)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown goal action %q", req.Action))
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	sess, err := s.sessionStore.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	sessions.NotifyUpdate(id)
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := sessions.Subscribe()
	defer sessions.Unsubscribe(ch)

	flusher, _ := w.(http.Flusher)
	writeSSE(w, "connected", "ok")
	if flusher != nil {
		flusher.Flush()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case sessionID := <-ch:
			writeSSE(w, "session_updated", sessionID)
			if flusher != nil {
				flusher.Flush()
			}
		case <-time.After(15 * time.Second):
			// Keep alive
			writeSSE(w, "heartbeat", "")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}
