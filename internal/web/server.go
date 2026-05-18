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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	mu        sync.RWMutex
	cfg       config.Config
	envPath   string
	client    *ollama.Client
	runner    *probe.Runner
	cachePath string
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
	OllamaBaseURL   string `json:"ollama_base_url"`
	WebAddr         string `json:"web_addr"`
	WebEnabled      bool   `json:"web_enabled"`
	ModelVision     string `json:"model_vision"`
	ModelAudio      string `json:"model_audio"`
	ModelEmbeddings string `json:"model_embeddings"`
}

type ChatRequest struct {
	Model    string           `json:"model"`
	Messages []ollama.Message `json:"messages"`
	Think    bool             `json:"think"`
}

func NewServer(cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string) *Server {
	return NewServerWithEnv(cfg, client, runner, cachePath, ".env")
}

func NewServerWithEnv(cfg config.Config, client *ollama.Client, runner *probe.Runner, cachePath string, envPath string) *Server {
	return &Server{cfg: cfg, envPath: envPath, client: client, runner: runner, cachePath: cachePath}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	cfg := s.config()
	log.Printf("ollamabot web listening on %s", cfg.WebAddr)
	return http.ListenAndServe(cfg.WebAddr, mux)
}

func (s *Server) config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) deps() (config.Config, *ollama.Client, *probe.Runner) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.client, s.runner
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, client, _ := s.deps()
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

	s.mu.Lock()
	s.cfg.OllamaBaseURL = baseURL
	s.cfg.OllamaModelVision = strings.TrimSpace(input.ModelVision)
	s.cfg.OllamaModelAudio = strings.TrimSpace(input.ModelAudio)
	s.cfg.OllamaModelEmbed = strings.TrimSpace(input.ModelEmbeddings)
	s.client = ollama.NewClient(baseURL)
	s.runner = probe.NewRunner(s.client)
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

	_, client, _ := s.deps()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	err := client.ChatStream(r.Context(), ollama.ChatRequest{
		Model:    input.Model,
		Messages: input.Messages,
		Think:    input.Think,
	}, func(chunk ollama.ChatResponse) error {
		if chunk.Message.Thinking != "" {
			writeSSE(w, "thinking", chunk.Message.Thinking)
		}
		if chunk.Message.Content != "" {
			writeSSE(w, "content", chunk.Message.Content)
		}
		for _, call := range chunk.Message.ToolCalls {
			writeSSE(w, "tool_call", call)
		}
		if chunk.Done {
			writeSSE(w, "done", map[string]any{"model": chunk.Model, "reason": chunk.DoneReason})
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		writeSSE(w, "error", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *Server) models(ctx context.Context) (ModelsResponse, error) {
	cfg, client, runner := s.deps()
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
		OllamaBaseURL:   cfg.OllamaBaseURL,
		WebAddr:         cfg.WebAddr,
		WebEnabled:      cfg.WebEnabled,
		ModelVision:     cfg.OllamaModelVision,
		ModelAudio:      cfg.OllamaModelAudio,
		ModelEmbeddings: cfg.OllamaModelEmbed,
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
