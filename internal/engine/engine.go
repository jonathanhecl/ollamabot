package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type StreamHandlerFactory func(recorder *sessions.Recorder, model string) agent.StreamHandler
type ImageProgressHandlerFactory func(recorder *sessions.Recorder) tools.ImageProgressHandler
type AttachmentHandlerFactory func(recorder *sessions.Recorder) tools.AttachmentGeneratedHandler
type PlanProgressHandler func(sessionID string, plan sessions.SessionPlan)

type Deps struct {
	Config       config.Config
	Client       *ollama.Client
	Runner       *probe.Runner
	SessionStore *sessions.Store
	MemoryStore  *memory.Store
	CachePath    string

	ApprovalHandler         tools.ApprovalHandler
	ClarificationHandler    tools.ClarificationHandler
	PlanConfirmationHandler tools.PlanConfirmationHandler
	StreamHandlerFactory    StreamHandlerFactory
	ImageProgressFactory    ImageProgressHandlerFactory
	AttachmentFactory       AttachmentHandlerFactory

	OnSleepActivity func()
	OnMediaResolved func(router.ResolveResult)
	OnPlanProgress  func(sessionID string, plan sessions.SessionPlan)
}

type TurnRequest struct {
	SessionID   string
	Channel     string
	Messages    []router.MediaMessage
	BaseHistory []sessions.RawMsg
}

type TurnResult struct {
	FinalHistory  []ollama.Message
	SavedMessages []json.RawMessage
	MediaResult   *router.ResolveResult
	ModelUsed     string
	FinalAnswer   string
}

func ProcessTurn(ctx context.Context, deps Deps, req TurnRequest) (TurnResult, error) {
	cfg := deps.Config
	model := config.ResolveModel(cfg, config.ModelRoleMain)
	if strings.TrimSpace(model) == "" {
		return TurnResult{}, fmt.Errorf("no default model configured")
	}
	if deps.Client == nil {
		return TurnResult{}, fmt.Errorf("ollama client is required")
	}

	if deps.OnSleepActivity != nil {
		deps.OnSleepActivity()
	}

	result := TurnResult{ModelUsed: model}
	mr := router.New(deps.Client, RouterConfig(cfg, model, deps.CachePath))
	mediaRes, err := mr.ResolveMessages(ctx, req.Messages)
	if err != nil {
		return result, err
	}
	result.MediaResult = &mediaRes
	if deps.OnMediaResolved != nil {
		deps.OnMediaResolved(mediaRes)
	}

	baseHistory := req.BaseHistory
	if len(baseHistory) == 0 && deps.SessionStore != nil && strings.TrimSpace(req.SessionID) != "" {
		baseHistory = LoadSessionRawMessages(deps.SessionStore, req.SessionID)
	}
	if deps.SessionStore != nil && strings.TrimSpace(req.SessionID) != "" && len(mediaRes.Attachments) > 0 {
		baseHistory = ApplyMediaMetadata(baseHistory, mediaRes.Attachments)
		if err := SaveRawMessages(deps.SessionStore, req.SessionID, baseHistory); err != nil {
			log.Printf("[Engine] Failed to persist media metadata for session %s: %v", req.SessionID, err)
		}
	}

	ollamaMessages := InjectContext(cfg.Workspace, cfg.SessionsPath, req.SessionID, mediaRes.Messages)
	ollamaMessages = InterceptImageCommand(ollamaMessages)

	recorder := sessions.NewRecorder(deps.SessionStore, req.SessionID, baseHistory, model, req.Channel)
	registry := BuildRegistry(deps, req.SessionID, recorder)
	handler := agent.StreamHandler(noopStreamHandler{})
	if deps.StreamHandlerFactory != nil {
		handler = deps.StreamHandlerFactory(recorder, model)
	}

	think := agent.ShouldThink(model, cfg.OllamaThinkEnabled, SnapshotPath(deps.CachePath))
	log.Printf("[Engine] Running turn channel=%q model=%q think=%v messages=%d", req.Channel, model, think, len(ollamaMessages))
	a := agent.NewAgent(cfg, deps.Client, registry)
	finalHistory, runErr := a.Run(ctx, model, ollamaMessages, think, handler)
	result.FinalHistory = finalHistory
	if runErr != nil {
		if _, saveErr := recorder.SnapshotAndSave(); saveErr != nil {
			log.Printf("[Engine] Failed to persist partial session snapshot: %v", saveErr)
		}
		return result, runErr
	}

	savedMessages, saveErr := recorder.FinalizeAndSave(finalHistory)
	if saveErr != nil {
		log.Printf("[Engine] Failed to persist final session snapshot: %v", saveErr)
	} else {
		result.SavedMessages = savedMessages
	}

	result.FinalAnswer = agent.CleanThinkingTokens(LastAssistantContent(finalHistory))
	if strings.TrimSpace(req.SessionID) != "" && cfg.SessionAutoName && result.FinalAnswer != "" {
		if err := AutoNameSession(ctx, cfg, deps.Client, deps.SessionStore, req.SessionID, result.FinalAnswer); err != nil {
			log.Printf("[Engine] Auto-name failed for session %s: %v", req.SessionID, err)
		}
	}

	return result, nil
}

func RouterConfig(cfg config.Config, mainModel string, cachePath string) router.Config {
	return router.Config{
		MainModel:     mainModel,
		VisionModel:   config.ResolveModel(cfg, config.ModelRoleVision),
		AudioModel:    config.ResolveModel(cfg, config.ModelRoleAudio),
		ImageModel:    config.ResolveModel(cfg, config.ModelRoleImage),
		ImageSteps:    cfg.OllamaImageSteps,
		HasCapability: cache.Checker(SnapshotPath(cachePath)),
	}
}

func BuildRegistry(deps Deps, sessionID string, recorder *sessions.Recorder) *tools.Registry {
	cfg := deps.Config
	registry := tools.NewRegistry(cfg.WebSearchEnabled, cfg.Workspace, deps.MemoryStore, deps.Client, config.ResolveModel(cfg, config.ModelRoleEmbed), tools.SearchConfig{
		Providers:    cfg.SearchProviders,
		BraveAPIKey:  cfg.BraveSearchAPIKey,
		TavilyAPIKey: cfg.TavilyAPIKey,
	})
	registry.SetApprovalHandler(deps.ApprovalHandler)
	registry.SetClarificationHandler(deps.ClarificationHandler)
	registry.SetPlanConfirmationHandler(deps.PlanConfirmationHandler)
	if deps.ImageProgressFactory != nil {
		registry.SetImageProgressHandler(deps.ImageProgressFactory(recorder))
	}
	if deps.AttachmentFactory != nil {
		registry.SetAttachmentGeneratedHandler(deps.AttachmentFactory(recorder))
	}
	registry.SetSessionsPath(cfg.SessionsPath)
	registry.SetSessionID(sessionID)
	registry.SetSessionStore(deps.SessionStore)
	if deps.OnPlanProgress != nil {
		registry.SetPlanProgressHandler(deps.OnPlanProgress)
	}
	return registry
}

func LoadSessionRawMessages(store *sessions.Store, sessionID string) []sessions.RawMsg {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	sess, err := store.Get(sessionID)
	if err != nil {
		return nil
	}
	out := make([]sessions.RawMsg, 0, len(sess.Messages))
	for _, raw := range sess.Messages {
		var msg sessions.RawMsg
		if err := json.Unmarshal(raw, &msg); err == nil {
			out = append(out, msg)
		}
	}
	return out
}

func MediaMessagesFromRaw(messages []sessions.RawMsg) []router.MediaMessage {
	out := make([]router.MediaMessage, 0, len(messages))
	for _, msg := range messages {
		var toolCalls []ollama.ToolCall
		for _, tcRaw := range msg.ToolCalls {
			var tc ollama.ToolCall
			if err := json.Unmarshal(tcRaw, &tc); err == nil {
				toolCalls = append(toolCalls, tc)
			}
		}
		var transcriptions []string
		for _, att := range msg.Attachments {
			if att.Kind == "audio" && strings.TrimSpace(att.Transcription) != "" {
				transcriptions = append(transcriptions, att.Transcription)
			}
		}
		out = append(out, router.MediaMessage{
			Message: ollama.Message{
				Role:      msg.Role,
				Content:   msg.Content,
				Thinking:  msg.Thinking,
				Images:    msg.Images,
				Name:      msg.Name,
				ToolCalls: toolCalls,
			},
			ImageKinds:     msg.ImageKinds,
			Transcriptions: transcriptions,
		})
	}
	return out
}

func SaveRawMessages(store *sessions.Store, sessionID string, messages []sessions.RawMsg) error {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	sess, err := store.Get(sessionID)
	if err != nil {
		return err
	}
	rawMessages := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		raw, _ := json.Marshal(msg)
		rawMessages = append(rawMessages, raw)
	}
	sess.Messages = rawMessages
	if err := store.Save(sess); err != nil {
		return err
	}
	sessions.NotifyUpdate(sessionID)
	return nil
}

func ApplyMediaMetadata(messages []sessions.RawMsg, attachments []router.AttachmentResult) []sessions.RawMsg {
	if len(messages) == 0 || len(attachments) == 0 {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		now := time.Now().Format(time.RFC3339)
		for _, ar := range attachments {
			if ar.Index < 0 || ar.Index >= len(messages[i].Attachments) {
				continue
			}
			att := &messages[i].Attachments[ar.Index]
			att.ProcessedBy = ar.Model
			att.ProcessedAt = now
			if ar.Kind == "audio" {
				att.Transcription = ar.Transcription
				att.Unreadable = ar.Unreadable
			}
			if ar.Kind == "image" {
				att.Description = ar.Description
			}
		}
		return messages
	}
	return messages
}

func LastAssistantContent(messages []ollama.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func InterceptImageCommand(messages []ollama.Message) []ollama.Message {
	lastIdx := len(messages) - 1
	if lastIdx < 0 || messages[lastIdx].Role != "user" {
		return messages
	}
	msgContent := strings.TrimSpace(messages[lastIdx].Content)
	if !strings.HasPrefix(strings.ToLower(msgContent), "/image ") {
		return messages
	}
	prompt := strings.TrimSpace(msgContent[len("/image "):])
	messages[lastIdx].Content = fmt.Sprintf("[SYSTEM FORCE IMAGE GENERATION: You MUST immediately call the 'generate_image' tool. The user has explicitly requested to imagine: %q. Translate this to an optimized, detailed English prompt for the image generation model. Do not return plain text response or start explaining; call the tool first.]", prompt)
	return messages
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

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func uploadsDir(workspace, sessionsPath, id string) string {
	if filepath.IsAbs(sessionsPath) {
		return filepath.Join(sessionsPath, id, "uploads")
	}
	return filepath.Join(workspace, sessionsPath, id, "uploads")
}

type noopStreamHandler struct{}

func (noopStreamHandler) OnThinking(string)                                {}
func (noopStreamHandler) OnContent(string)                                 {}
func (noopStreamHandler) OnToolCall(ollama.ToolCall)                       {}
func (noopStreamHandler) OnToolStart(string, any)                          {}
func (noopStreamHandler) OnToolResult(string, string)                      {}
func (noopStreamHandler) OnMediaPreProcessing(string)                      {}
func (noopStreamHandler) OnDone(ollama.ChatResponse)                       {}
func (noopStreamHandler) OnContextOptimizationStart(int, float64)          {}
func (noopStreamHandler) OnContextOptimizationEnd(int, float64, float64)   {}
func (noopStreamHandler) OnContextOptimized([]ollama.Message, string, int) {}
