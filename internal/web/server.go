package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
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
	mu           sync.RWMutex
	cfg          config.Config
	envPath      string
	client       *ollama.Client
	runner       *probe.Runner
	mediaro      *router.Router
	cachePath    string
	sessionStore *sessions.Store
	memoryStore  *memory.Store
	autoMgr      *agent.AutonomousManager
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
	OllamaBaseURL       string `json:"ollama_base_url"`
	ServerPort          string `json:"server_port"`
	ServerEnabled       bool   `json:"server_enabled"`
	WebSearchEnabled    bool   `json:"web_search_enabled"`
	ServerExposeNetwork bool   `json:"server_expose_network"`
	SessionAutoName     bool   `json:"session_auto_name"`
	ModelDefault        string `json:"model_default"`
	ModelVision         string `json:"model_vision"`
	ModelAudio          string `json:"model_audio"`
	ModelEmbeddings     string `json:"model_embeddings"`
	Workspace           string `json:"workspace"`
	SessionsPath        string `json:"sessions_path"`
	MemoryPath          string `json:"memory_path"`
}

// MediaMessage extends ollama.Message with per-image kind metadata sent by the
// frontend. ImageKinds[i] is "image" or "audio" for Images[i].
type MediaMessage struct {
	ollama.Message
	ImageKinds []string `json:"image_kinds,omitempty"`
}

type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []MediaMessage `json:"messages"`
	Think    bool           `json:"think"`
}

func NewServer(cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string) *Server {
	return NewServerWithEnv(cfg, client, runner, cachePath, ".env")
}

func NewServerWithEnv(cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string, envPath string) *Server {
	mr := router.New(client, routerConfig(cfg))
	ss := sessions.NewStore(cfg.SessionsPath)
	ms := memory.NewStore(cfg.MemoryPath)
	am := agent.NewAutonomousManager(cfg, client, ms)
	return &Server{cfg: cfg, envPath: envPath, client: client, runner: runner, mediaro: mr, cachePath: cachePath, sessionStore: ss, memoryStore: ms, autoMgr: am}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("PUT /api/sessions/{id}", s.handleUpdateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/memory", s.handleListMemory)
	mux.HandleFunc("POST /api/memory", s.handleAddMemory)
	mux.HandleFunc("POST /api/memory/search", s.handleSearchMemory)
	mux.HandleFunc("DELETE /api/memory/{id}", s.handleDeleteMemory)
	mux.HandleFunc("GET /api/health", s.handleHealth)

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
	return http.ListenAndServe(addr, mux)
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
	s.cfg.WebSearchEnabled = input.WebSearchEnabled
	s.cfg.ServerExposeNetwork = input.ServerExposeNetwork
	s.cfg.SessionAutoName = input.SessionAutoName
	s.cfg.Workspace = workspace
	s.cfg.SessionsPath = sessionsPath
	s.cfg.MemoryPath = memoryPath
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

func routerConfig(cfg config.Config) router.Config {
	return router.Config{
		MainModel:   cfg.OllamaDefaultModel,
		VisionModel: cfg.OllamaModelVision,
		AudioModel:  cfg.OllamaModelAudio,
	}
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
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

	cfg, client, _, mr := s.deps()

	// Log incoming request summary for debugging media routing
	for i, msg := range input.Messages {
		if len(msg.Images) > 0 {
			log.Printf("[handleChatStream] Message[%d]: role=%q, content_len=%d, images=%d, image_kinds=%v",
				i, msg.Role, len(msg.Content), len(msg.Images), msg.ImageKinds)
		}
	}

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

	// Inject autonomous memory management instruction as a system prompt.
	if cfg.OllamaModelEmbed != "" {
		ollamaMessages = append([]ollama.Message{{
			Role:    "system",
			Content: "You have access to long-term memory tools (memory_add, memory_search, memory_delete, memory_list). Manage your own memory proactively:\n- Store important facts, user preferences, decisions, and context using memory_add.\n- Search memory when the question may benefit from past knowledge using memory_search.\n- Delete outdated or incorrect information using memory_delete.\n- Review stored memories with memory_list before deciding what to add, update, or remove.\n- Consolidate: if you learn updated information, delete the old version and store the new one.\n- Prioritize: only store information that is likely to be useful later.",
		}}, ollamaMessages...)
	}

	// Dynamically acquire personality/name from the latest user message
	if len(input.Messages) > 0 {
		lastMsg := input.Messages[len(input.Messages)-1]
		if lastMsg.Role == "user" {
			_ = agent.UpdateSoulFromPrompt(lastMsg.Content)
		}
	}

	// Prepend SOUL.md system instruction at the very top.
	if soulContent, err := agent.LoadSoul(); err == nil && soulContent != "" {
		ollamaMessages = append([]ollama.Message{{
			Role:    "system",
			Content: soulContent,
		}}, ollamaMessages...)
	}

	registry := tools.NewRegistry(cfg.WebSearchEnabled, cfg.Workspace, s.memoryStore, client, cfg.OllamaModelEmbed)

	err = runChatStream(r.Context(), cfg, client, input.Model, ollamaMessages, input.Think, registry, w, flusher)
	if err != nil {
		writeSSE(w, "error", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
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
	writeSSE(h.w, "tool_start", map[string]any{"name": name, "arguments": args})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnToolResult(name string, result string) {
	writeSSE(h.w, "tool_result", map[string]any{"name": name, "result": result})
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func (h *sseStreamHandler) OnMediaPreProcessing(content string) {
	writeSSE(h.w, "media_pre_processing", content)
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

// resolveMedia iterates the messages, and for any user message that has media
// attachments handled by a dedicated role model, it invokes the role model to
// produce a textual analysis. The analysis is injected as an assistant message,
// followed by the original user message (with the user's text, if any, and any
// media that did not need routing). This ensures the main model understands the
// analysis as context from another model, not as text sent by the user.
func resolveMedia(ctx context.Context, mr *router.Router, messages []MediaMessage, w http.ResponseWriter, flusher http.Flusher) ([]ollama.Message, error) {
	out := make([]ollama.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "user" || len(msg.Images) == 0 {
			out = append(out, msg.Message)
			continue
		}

		var analyses []string
		var passthrough []string
		var audioTranscriptions []string

		type attachment struct {
			kind   string
			base64 string
		}

		var attachments []attachment
		for i, b64 := range msg.Images {
			kind := "image"
			if i < len(msg.ImageKinds) {
				kind = msg.ImageKinds[i]
			}
			attachments = append(attachments, attachment{
				kind:   kind,
				base64: b64,
			})
		}

		log.Printf("[resolveMedia] User message has %d attachment(s), imageKinds=%v, content=%q", len(attachments), msg.ImageKinds, truncate(msg.Content, 100))
		for i, att := range attachments {
			log.Printf("[resolveMedia]   attachment[%d]: kind=%q, data_len=%d", i, att.kind, len(att.base64))
		}

		// Pass 1: Process routed audio attachments first
		for _, att := range attachments {
			if att.kind == "audio" {
				needsRouting := mr.NeedsMediaRouting(att.kind)
				log.Printf("[resolveMedia] Audio attachment: needsRouting=%v, data_len=%d", needsRouting, len(att.base64))
				if needsRouting {
					// Audio gets the user's text content as its analysis prompt
					log.Printf("[resolveMedia] Sending audio to dedicated audio model for analysis...")
					analysis, err := mr.AnalyzeAudio(ctx, att.base64, msg.Content)
					if err != nil {
						log.Printf("[resolveMedia] Audio analysis FAILED: %v", err)
						return nil, err
					}
					log.Printf("[resolveMedia] Audio analysis result (len=%d): %s", len(analysis), truncate(analysis, 200))
					audioTranscriptions = append(audioTranscriptions, analysis)
					analyses = append(analyses, fmt.Sprintf("[Audio Transcription & Analysis]:\n%s", analysis))
				} else {
					log.Printf("[resolveMedia] Audio goes to passthrough (main model handles it natively)")
					passthrough = append(passthrough, att.base64)
				}
			}
		}

		// Construct image prompt by combining text prompt and audio transcriptions
		imagePrompt := msg.Content
		if len(audioTranscriptions) > 0 {
			combinedAudio := strings.Join(audioTranscriptions, "\n\n")
			if strings.TrimSpace(imagePrompt) != "" {
				imagePrompt = fmt.Sprintf("%s\n\n[Instruction/Context from Audio Transcription]:\n%s", imagePrompt, combinedAudio)
			} else {
				imagePrompt = fmt.Sprintf("Analyze this image based on the following instruction transcribed from audio:\n%s", combinedAudio)
			}
			log.Printf("[resolveMedia] Image prompt augmented with audio transcription: %s", truncate(imagePrompt, 200))
		}

		// Pass 2: Process image attachments
		for _, att := range attachments {
			if att.kind != "audio" {
				needsRouting := mr.NeedsMediaRouting(att.kind)
				log.Printf("[resolveMedia] Image attachment: needsRouting=%v, data_len=%d", needsRouting, len(att.base64))
				if needsRouting {
					log.Printf("[resolveMedia] Sending image to dedicated vision model for analysis...")
					analysis, err := mr.AnalyzeImage(ctx, att.base64, imagePrompt)
					if err != nil {
						log.Printf("[resolveMedia] Image analysis FAILED: %v", err)
						return nil, err
					}
					log.Printf("[resolveMedia] Image analysis result (len=%d): %s", len(analysis), truncate(analysis, 200))
					// Truncate instruction preview for assistant message log readability
					logPrompt := imagePrompt
					if len(logPrompt) > 120 {
						logPrompt = logPrompt[:117] + "..."
					}
					analyses = append(analyses, fmt.Sprintf("[Image Analysis (Prompt: %s)]:\n%s", strings.ReplaceAll(logPrompt, "\n", " "), analysis))
				} else {
					log.Printf("[resolveMedia] Image goes to passthrough (main model handles it natively)")
					passthrough = append(passthrough, att.base64)
				}
			}
		}

		log.Printf("[resolveMedia] Summary: %d analyses, %d passthrough, %d audioTranscriptions", len(analyses), len(passthrough), len(audioTranscriptions))

		if len(analyses) > 0 {
			assistantContent := "The user has attached media. The pre-processing analysis is as follows:\n\n" + strings.Join(analyses, "\n\n")
			if w != nil {
				writeSSE(w, "media_pre_processing", assistantContent)
				if flusher != nil {
					flusher.Flush()
				}
			}
			out = append(out, ollama.Message{
				Role:    "assistant",
				Content: assistantContent,
			})
		}

		resolved := msg.Message
		resolved.Images = passthrough

		// Format and inject the audio transcription contextually into the final user prompt
		if len(audioTranscriptions) > 0 {
			combinedAudio := strings.Join(audioTranscriptions, "\n\n")
			hasPassthroughImages := len(passthrough) > 0
			hasUserContent := strings.TrimSpace(resolved.Content) != ""

			if hasPassthroughImages {
				if hasUserContent {
					resolved.Content = fmt.Sprintf("%s\n\n[The user also sent this audio transcription accompanying the image]:\n\"%s\"", resolved.Content, combinedAudio)
				} else {
					resolved.Content = fmt.Sprintf("[The user sent this audio transcription accompanying the image]:\n\"%s\"", combinedAudio)
				}
			} else {
				if hasUserContent {
					resolved.Content = fmt.Sprintf("%s\n\n[The user also sent this audio transcription]:\n\"%s\"", resolved.Content, combinedAudio)
				} else {
					resolved.Content = fmt.Sprintf("[The user sent only this audio transcription]:\n\"%s\"", combinedAudio)
				}
			}
		}

		log.Printf("[resolveMedia] Final resolved message: role=%q, content_len=%d, images=%d", resolved.Role, len(resolved.Content), len(resolved.Images))
		out = append(out, resolved)
	}
	return out, nil
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
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sessionStore.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
	if err := s.memoryStore.Add(entry); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
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
	return SettingsResponse{
		OllamaBaseURL:       cfg.OllamaBaseURL,
		ServerPort:          cfg.ServerPort,
		ServerEnabled:       cfg.ServerEnabled,
		WebSearchEnabled:    cfg.WebSearchEnabled,
		ServerExposeNetwork: cfg.ServerExposeNetwork,
		SessionAutoName:     cfg.SessionAutoName,
		ModelDefault:        cfg.OllamaDefaultModel,
		ModelVision:         cfg.OllamaModelVision,
		ModelAudio:          cfg.OllamaModelAudio,
		ModelEmbeddings:     cfg.OllamaModelEmbed,
		Workspace:           cfg.Workspace,
		SessionsPath:        cfg.SessionsPath,
		MemoryPath:          cfg.MemoryPath,
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
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
	logPath := filepath.Join(s.cfg.Workspace, id, "logs", logName)
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
