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

	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
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
	OllamaBaseURL    string `json:"ollama_base_url"`
	WebAddr          string `json:"web_addr"`
	WebEnabled       bool   `json:"web_enabled"`
	WebSearchEnabled bool   `json:"web_search_enabled"`
	WebExposeNetwork bool   `json:"web_expose_network"`
	ModelVision      string `json:"model_vision"`
	ModelAudio       string `json:"model_audio"`
	ModelEmbeddings  string `json:"model_embeddings"`
	Workspace        string `json:"workspace"`
	SessionsPath     string `json:"sessions_path"`
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
	return &Server{cfg: cfg, envPath: envPath, client: client, runner: runner, mediaro: mr, cachePath: cachePath, sessionStore: ss}
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
	mux.HandleFunc("GET /api/health", s.handleHealth)

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	cfg := s.config()
	addr := cfg.WebAddr
	if !cfg.WebExposeNetwork && strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
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

	s.mu.Lock()
	s.cfg.OllamaBaseURL = baseURL
	s.cfg.OllamaModelVision = strings.TrimSpace(input.ModelVision)
	s.cfg.OllamaModelAudio = strings.TrimSpace(input.ModelAudio)
	s.cfg.OllamaModelEmbed = strings.TrimSpace(input.ModelEmbeddings)
	s.cfg.WebSearchEnabled = input.WebSearchEnabled
	s.cfg.WebExposeNetwork = input.WebExposeNetwork
	s.cfg.Workspace = workspace
	s.cfg.SessionsPath = sessionsPath
	s.client = ollama.NewClient(baseURL)
	s.runner = probe.NewRunner(s.client)
	s.mediaro = router.New(s.client, routerConfig(s.cfg))
	s.sessionStore = sessions.NewStore(sessionsPath)
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

	// Pre-process media attachments using role models before sending to main.
	ollamaMessages, err := resolveMedia(r.Context(), mr, input.Messages)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	registry := tools.NewRegistry(cfg.WebSearchEnabled, cfg.Workspace)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	err = runChatStream(r.Context(), client, input.Model, ollamaMessages, input.Think, registry, w, flusher)
	if err != nil {
		writeSSE(w, "error", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// runChatStream handles the chat streaming loop including tool calls.
func runChatStream(ctx context.Context, client *ollama.Client, model string, messages []ollama.Message, think bool, registry *tools.Registry, w http.ResponseWriter, flusher http.Flusher) error {
	maxToolRounds := 3
	for round := 0; round <= maxToolRounds; round++ {
		var assistantContent strings.Builder
		var assistantThinking strings.Builder
		var toolCalls []ollama.ToolCall
		seenTool := map[string]struct{}{}

		req := ollama.ChatRequest{
			Model:    model,
			Messages: messages,
			Think:    think,
		}
		defs := registry.Definitions()
		if len(defs) > 0 && round < maxToolRounds {
			req.Tools = defs
		}

		done := false
		err := client.ChatStream(ctx, req, func(chunk ollama.ChatResponse) error {
			if chunk.Message.Thinking != "" {
				assistantThinking.WriteString(chunk.Message.Thinking)
				writeSSE(w, "thinking", chunk.Message.Thinking)
			}
			if chunk.Message.Content != "" {
				assistantContent.WriteString(chunk.Message.Content)
				writeSSE(w, "content", chunk.Message.Content)
			}
			for _, call := range chunk.Message.ToolCalls {
				key := call.Function.Name + "|" + string(call.Function.Arguments)
				if _, ok := seenTool[key]; ok {
					continue
				}
				seenTool[key] = struct{}{}
				toolCalls = append(toolCalls, call)
				writeSSE(w, "tool_call", call)
			}
			if chunk.Done {
				done = true
				writeSSE(w, "done", map[string]any{"model": chunk.Model, "reason": chunk.DoneReason})
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !done {
			return nil
		}
		if len(toolCalls) == 0 {
			return nil
		}

		// Build assistant message with tool calls.
		assistantMsg := ollama.Message{
			Role:      "assistant",
			Content:   assistantContent.String(),
			ToolCalls: toolCalls,
		}
		messages = append(messages, assistantMsg)

		// Execute tools and append results.
		for _, call := range toolCalls {
			writeSSE(w, "tool_start", map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments})
			if flusher != nil {
				flusher.Flush()
			}
			result, terr := registry.Execute(ctx, call)
			if terr != nil {
				result = fmt.Sprintf("Error: %v", terr)
			}
			writeSSE(w, "tool_result", map[string]any{"name": call.Function.Name, "result": result})
			if flusher != nil {
				flusher.Flush()
			}
			messages = append(messages, ollama.Message{
				Role:    "tool",
				Name:    call.Function.Name,
				Content: result,
			})
		}

		// Continue loop to get model's final response using tool results.
	}
	return nil
}

// resolveMedia iterates the messages, and for any user message that has media
// attachments handled by a dedicated role model, it invokes the role model to
// produce a textual analysis. The analysis is injected as an assistant message,
// followed by the original user message (with the user's text, if any, and any
// media that did not need routing). This ensures the main model understands the
// analysis as context from another model, not as text sent by the user.
func resolveMedia(ctx context.Context, mr *router.Router, messages []MediaMessage) ([]ollama.Message, error) {
	out := make([]ollama.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "user" || len(msg.Images) == 0 {
			out = append(out, msg.Message)
			continue
		}

		var analyses []string
		var passthrough []string

		for i, b64 := range msg.Images {
			kind := "image"
			if i < len(msg.ImageKinds) {
				kind = msg.ImageKinds[i]
			}
			if mr.NeedsMediaRouting(kind) {
				var analysis string
				var err error
				if kind == "audio" {
					analysis, err = mr.AnalyzeAudio(ctx, b64)
				} else {
					analysis, err = mr.AnalyzeImage(ctx, b64)
				}
				if err != nil {
					return nil, err
				}
				analyses = append(analyses, analysis)
			} else {
				passthrough = append(passthrough, b64)
			}
		}

		if len(analyses) > 0 {
			assistantContent := "The user has attached media. The analysis says the following:\n\n" + strings.Join(analyses, "\n\n")
			out = append(out, ollama.Message{
				Role:    "assistant",
				Content: assistantContent,
			})
		}

		resolved := msg.Message
		resolved.Images = passthrough
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
		OllamaBaseURL:    cfg.OllamaBaseURL,
		WebAddr:          cfg.WebAddr,
		WebEnabled:       cfg.WebEnabled,
		WebSearchEnabled: cfg.WebSearchEnabled,
		WebExposeNetwork: cfg.WebExposeNetwork,
		ModelVision:      cfg.OllamaModelVision,
		ModelAudio:       cfg.OllamaModelAudio,
		ModelEmbeddings:  cfg.OllamaModelEmbed,
		Workspace:        cfg.Workspace,
		SessionsPath:     cfg.SessionsPath,
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
