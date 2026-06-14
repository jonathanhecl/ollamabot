package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type GoalManager struct {
	mu           sync.Mutex
	cfg          config.Config
	client       *ollama.Client
	sessionStore *sessions.Store
	memoryStore  *memory.Store
	activeLoops  map[string]context.CancelFunc
	notifiers    map[string]func(message string)
	notifiersMu  sync.RWMutex
}

func NewGoalManager(cfg config.Config, client *ollama.Client) *GoalManager {
	ss := sessions.NewStore(cfg.SessionsPath)
	ms := memory.NewStore(cfg.MemoryPath)
	return &GoalManager{
		cfg:          cfg,
		client:       client,
		sessionStore: ss,
		memoryStore:  ms,
		activeLoops:  make(map[string]context.CancelFunc),
		notifiers:    make(map[string]func(message string)),
	}
}

func (g *GoalManager) ResumeActiveGoals() error {
	sessList, err := g.sessionStore.List()
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, sMeta := range sessList {
		if sMeta.GoalStatus == "active" && sMeta.GoalObjective != "" {
			log.Printf("[GoalManager] Resuming active goal for session %s on startup", sMeta.ID)
			ctx, cancel := context.WithCancel(context.Background())
			g.activeLoops[sMeta.ID] = cancel
			go g.runGoalLoop(ctx, sMeta.ID, sMeta.GoalObjective)
		}
	}
	return nil
}

func (g *GoalManager) RegisterNotifier(sessionID string, callback func(message string)) {
	g.notifiersMu.Lock()
	defer g.notifiersMu.Unlock()
	g.notifiers[sessionID] = callback
}

func (g *GoalManager) UnregisterNotifier(sessionID string) {
	g.notifiersMu.Lock()
	defer g.notifiersMu.Unlock()
	delete(g.notifiers, sessionID)
}

func (g *GoalManager) notify(sessionID string, message string) {
	g.notifiersMu.RLock()
	callback, ok := g.notifiers[sessionID]
	g.notifiersMu.RUnlock()
	if ok && callback != nil {
		go callback(message)
	}
}

func (g *GoalManager) StartGoal(sessionID string, objective string) error {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return fmt.Errorf("goal objective cannot be empty")
	}
	if len(objective) > 4000 {
		return fmt.Errorf("goal objective cannot exceed 4000 characters")
	}

	// Try loading the objective from a file if it refers to an existing file in the workspace
	if !strings.Contains(objective, "\n") && len(objective) < 260 {
		filePath := filepath.Join(g.cfg.Workspace, objective)
		if _, err := os.Stat(filePath); err == nil {
			if data, err := os.ReadFile(filePath); err == nil {
				objective = string(data)
				log.Printf("[GoalManager] Loaded objective from workspace file: %s", objective)
			}
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Stop any existing loop
	if cancel, ok := g.activeLoops[sessionID]; ok {
		cancel()
		delete(g.activeLoops, sessionID)
	}

	sess, err := g.sessionStore.Get(sessionID)
	if err != nil {
		return err
	}

	sess.GoalObjective = objective
	sess.GoalStatus = "active"
	sess.GoalReasoning = "Initializing objective evaluation..."

	// Append starting status message to the session
	startMsg := sessions.RawMsg{
		Role:      "system",
		Content:   fmt.Sprintf("🎯 *Goal Started:* %s", truncate(objective, 150)),
		Timestamp: time.Now().Format(time.RFC3339),
	}
	rawStartMsg, _ := json.Marshal(startMsg)
	sess.Messages = append(sess.Messages, rawStartMsg)

	if err := g.sessionStore.Save(sess); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	g.activeLoops[sessionID] = cancel

	go g.runGoalLoop(ctx, sessionID, objective)
	return nil
}

func (g *GoalManager) PauseGoal(sessionID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if cancel, ok := g.activeLoops[sessionID]; ok {
		cancel()
		delete(g.activeLoops, sessionID)
	}

	sess, err := g.sessionStore.Get(sessionID)
	if err != nil {
		return err
	}

	if sess.GoalStatus != "active" {
		return fmt.Errorf("goal is not active")
	}

	sess.GoalStatus = "paused"
	pauseMsg := sessions.RawMsg{
		Role:      "system",
		Content:   "⏸️ *Goal Paused*",
		Timestamp: time.Now().Format(time.RFC3339),
	}
	rawPauseMsg, _ := json.Marshal(pauseMsg)
	sess.Messages = append(sess.Messages, rawPauseMsg)

	if err := g.sessionStore.Save(sess); err != nil {
		return err
	}

	g.notify(sessionID, "⏸️ Goal has been paused.")
	return nil
}

func (g *GoalManager) ResumeGoal(sessionID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if cancel, ok := g.activeLoops[sessionID]; ok {
		cancel()
		delete(g.activeLoops, sessionID)
	}

	sess, err := g.sessionStore.Get(sessionID)
	if err != nil {
		return err
	}

	if sess.GoalStatus != "paused" {
		return fmt.Errorf("goal is not paused")
	}

	sess.GoalStatus = "active"
	resumeMsg := sessions.RawMsg{
		Role:      "system",
		Content:   "▶️ *Goal Resumed*",
		Timestamp: time.Now().Format(time.RFC3339),
	}
	rawResumeMsg, _ := json.Marshal(resumeMsg)
	sess.Messages = append(sess.Messages, rawResumeMsg)

	if err := g.sessionStore.Save(sess); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	g.activeLoops[sessionID] = cancel

	go g.runGoalLoop(ctx, sessionID, sess.GoalObjective)
	g.notify(sessionID, "▶️ Resuming goal background execution...")
	return nil
}

func (g *GoalManager) ClearGoal(sessionID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if cancel, ok := g.activeLoops[sessionID]; ok {
		cancel()
		delete(g.activeLoops, sessionID)
	}

	sess, err := g.sessionStore.Get(sessionID)
	if err != nil {
		return err
	}

	sess.GoalStatus = ""
	sess.GoalObjective = ""
	sess.GoalReasoning = ""

	clearMsg := sessions.RawMsg{
		Role:      "system",
		Content:   "🧹 *Goal Cleared*",
		Timestamp: time.Now().Format(time.RFC3339),
	}
	rawClearMsg, _ := json.Marshal(clearMsg)
	sess.Messages = append(sess.Messages, rawClearMsg)

	if err := g.sessionStore.Save(sess); err != nil {
		return err
	}

	g.notify(sessionID, "🧹 Goal cleared.")
	return nil
}

func (g *GoalManager) runGoalLoop(ctx context.Context, sessionID string, objective string) {
	log.Printf("[GoalManager] Starting background loop for session: %s, goal: %q", sessionID, objective)
	maxCycles := 20
	cycleDelay := 5 * time.Second

	for cycle := 1; cycle <= maxCycles; cycle++ {
		select {
		case <-ctx.Done():
			log.Printf("[GoalManager] Context cancelled, stopping loop for session: %s", sessionID)
			return
		default:
		}

		// Reload session to inspect status changes
		sess, err := g.sessionStore.Get(sessionID)
		if err != nil {
			log.Printf("[GoalManager] Error reloading session: %v. Stopping loop.", err)
			return
		}

		if sess.GoalStatus != "active" {
			log.Printf("[GoalManager] GoalStatus changed to %s. Stopping loop.", sess.GoalStatus)
			return
		}

		log.Printf("[GoalManager] Session %s executing cycle %d/%d...", sessionID, cycle, maxCycles)

		// Convert session messages to agent loop input
		var ollamaMessages []ollama.Message
		for _, raw := range sess.Messages {
			var m sessions.RawMsg
			if err := json.Unmarshal(raw, &m); err == nil {
				// We don't feed back raw media base64 images inside background loop to save VRAM
				var toolCalls []ollama.ToolCall
				for _, tcRaw := range m.ToolCalls {
					var tc ollama.ToolCall
					if err := json.Unmarshal(tcRaw, &tc); err == nil {
						toolCalls = append(toolCalls, tc)
					}
				}
				ollamaMessages = append(ollamaMessages, ollama.Message{
					Role:      m.Role,
					Content:   m.Content,
					Thinking:  m.Thinking,
					Name:      m.Name,
					ToolCalls: toolCalls,
				})
			}
		}

		// Setup tool registry
		registry := tools.NewRegistry(g.cfg.WebSearchEnabled, g.cfg.Workspace, g.memoryStore, g.client, g.cfg.OllamaModelEmbed, tools.SearchConfig{
			Providers:    g.cfg.SearchProviders,
			BraveAPIKey:  g.cfg.BraveSearchAPIKey,
			TavilyAPIKey: g.cfg.TavilyAPIKey,
		})

		a := NewAgent(g.cfg, g.client, registry)
		handler := &goalStreamHandler{
			sessionStore: g.sessionStore,
			sessionID:    sessionID,
			baseMessages: sess.Messages,
		}

		// Run execution turn with smart retry
		var finalHistory []ollama.Message
		var runErr error
		maxRunRetries := 3
		for retry := 0; retry < maxRunRetries; retry++ {
			finalHistory, runErr = a.Run(ctx, g.cfg.OllamaDefaultModel, ollamaMessages, true, handler)
			if runErr == nil {
				break
			}
			log.Printf("[GoalManager] Error running cycle %d (attempt %d/%d): %v", cycle, retry+1, maxRunRetries, runErr)
			if retry < maxRunRetries-1 {
				g.notify(sessionID, fmt.Sprintf("⚠️ Goal loop execution warning: %v. Retrying in 10 seconds...", runErr))
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
				// Reload session to make sure we have the latest state before retrying
				sess, err = g.sessionStore.Get(sessionID)
				if err != nil {
					log.Printf("[GoalManager] Error reloading session for retry: %v. Aborting.", err)
					return
				}
				// Re-instantiate agent and registry to ensure fresh state if needed
				registry = tools.NewRegistry(g.cfg.WebSearchEnabled, g.cfg.Workspace, g.memoryStore, g.client, g.cfg.OllamaModelEmbed, tools.SearchConfig{
					Providers:    g.cfg.SearchProviders,
					BraveAPIKey:  g.cfg.BraveSearchAPIKey,
					TavilyAPIKey: g.cfg.TavilyAPIKey,
				})
				a = NewAgent(g.cfg, g.client, registry)
			}
		}

		if runErr != nil {
			log.Printf("[GoalManager] Error running cycle %d after %d attempts: %v", cycle, maxRunRetries, runErr)
			g.notify(sessionID, fmt.Sprintf("❌ Goal loop execution error: %v", runErr))
			return
		}

		// Sync final history with session
		var newRawMessages []json.RawMessage
		for _, m := range finalHistory {
			var tcRaw []json.RawMessage
			for _, tc := range m.ToolCalls {
				tcBytes, _ := json.Marshal(tc)
				tcRaw = append(tcRaw, tcBytes)
			}
			rm := sessions.RawMsg{
				Role:      m.Role,
				Content:   m.Content,
				Thinking:  m.Thinking,
				Name:      m.Name,
				ToolCalls: tcRaw,
				Timestamp: time.Now().Format(time.RFC3339),
			}
			rawBytes, _ := json.Marshal(rm)
			newRawMessages = append(newRawMessages, rawBytes)
		}

		sess.Messages = newRawMessages
		_ = g.sessionStore.Save(sess)

		// Run evaluation turn
		log.Printf("[GoalManager] Evaluating goal status after cycle %d...", cycle)
		achieved, reasoning, err := g.evaluateProgress(ctx, objective, sess.Messages)
		if err != nil {
			log.Printf("[GoalManager] Evaluation error: %v. Retrying in next cycle.", err)
			reasoning = "Evaluation system error: " + err.Error()
		}

		sess, _ = g.sessionStore.Get(sessionID) // reload
		sess.GoalReasoning = reasoning

		if achieved {
			sess.GoalStatus = "completed"
			completionMsg := sessions.RawMsg{
				Role:      "system",
				Content:   fmt.Sprintf("🎯 *Goal Completed:* %s", reasoning),
				Timestamp: time.Now().Format(time.RFC3339),
			}
			rawCompMsg, _ := json.Marshal(completionMsg)
			sess.Messages = append(sess.Messages, rawCompMsg)
			_ = g.sessionStore.Save(sess)

			g.notify(sessionID, fmt.Sprintf("🎯 *Goal achieved!* \n%s", reasoning))
			log.Printf("[GoalManager] Goal fully achieved: %s", reasoning)
			return
		}

		log.Printf("[GoalManager] Goal not achieved yet. Reasoning: %s", reasoning)

		// If max cycles reached, fail the goal
		if cycle == maxCycles {
			sess.GoalStatus = "failed"
			failureMsg := sessions.RawMsg{
				Role:      "system",
				Content:   fmt.Sprintf("❌ *Goal Failed:* Maximum iterations (%d) reached. Last check: %s", maxCycles, reasoning),
				Timestamp: time.Now().Format(time.RFC3339),
			}
			rawFailMsg, _ := json.Marshal(failureMsg)
			sess.Messages = append(sess.Messages, rawFailMsg)
			_ = g.sessionStore.Save(sess)

			g.notify(sessionID, fmt.Sprintf("❌ *Goal failed.* Maximum cycles reached. last reason: %s", reasoning))
			return
		}

		// Update goal reasoning status in system message and alert user
		checkMsg := sessions.RawMsg{
			Role:      "system",
			Content:   fmt.Sprintf("🔍 *Goal Check (Cycle %d/%d):* %s", cycle, maxCycles, reasoning),
			Timestamp: time.Now().Format(time.RFC3339),
		}
		rawCheckMsg, _ := json.Marshal(checkMsg)
		sess.Messages = append(sess.Messages, rawCheckMsg)

		// Feed the evaluator response back to the agent for the next turn
		feedbackMsg := sessions.RawMsg{
			Role:      "user",
			Content:   fmt.Sprintf("Evaluator Check: Goal not achieved yet. Criterias left: %s. Please run tools or take actions to complete the objective.", reasoning),
			Timestamp: time.Now().Format(time.RFC3339),
		}
		rawFeedbackMsg, _ := json.Marshal(feedbackMsg)
		sess.Messages = append(sess.Messages, rawFeedbackMsg)

		_ = g.sessionStore.Save(sess)
		g.notify(sessionID, fmt.Sprintf("🔍 *Goal progress check (Cycle %d):* %s", cycle, reasoning))

		// Wait before executing the next cycle
		select {
		case <-ctx.Done():
			return
		case <-time.After(cycleDelay):
		}
	}
}

type progressEvaluation struct {
	Achieved  bool   `json:"achieved"`
	Reasoning string `json:"reasoning"`
}

func (g *GoalManager) evaluateProgress(ctx context.Context, objective string, messages []json.RawMessage) (bool, string, error) {
	modelToUse := g.cfg.OllamaModelSubagent
	if strings.TrimSpace(modelToUse) == "" {
		modelToUse = g.cfg.OllamaDefaultModel
	}

	var historyText strings.Builder
	for idx, raw := range messages {
		var m sessions.RawMsg
		if err := json.Unmarshal(raw, &m); err == nil {
			if m.Role == "system" {
				continue
			}
			fmt.Fprintf(&historyText, "[Msg %d] %s: %s\n", idx+1, m.Role, m.Content)
			if len(m.ToolCalls) > 0 {
				fmt.Fprintf(&historyText, "  Tool calls: %d calls made.\n", len(m.ToolCalls))
			}
		}
	}

	evalPrompt := fmt.Sprintf(`You are the OllamaBot Goal Evaluator.
Analyze the conversation history and the actions executed so far.
The user's objective is: "%s"

Your task is to determine whether the objective has been fully achieved.
Return ONLY a valid JSON object matching the schema below:
{
  "achieved": true/false,
  "reasoning": "A concise explanation of why the objective is or is not fully achieved, detailing what is missing if false."
}
Do NOT include any markdown code blocks, packaging, or conversational filler. Return only valid JSON.`, objective)

	req := ollama.ChatRequest{
		Model: modelToUse,
		Messages: []ollama.Message{
			{Role: "system", Content: evalPrompt},
			{Role: "user", Content: fmt.Sprintf("Analyze conversation history:\n\n%s\n\nReturn evaluation JSON.", historyText.String())},
		},
		Format: "json", // Enforce structured output from Ollama if supported
	}

	resp, err := g.client.Chat(ctx, req)
	if err != nil {
		return false, "", err
	}

	rawText := strings.TrimSpace(resp.Message.Content)
	rawText = strings.TrimPrefix(rawText, "```json")
	rawText = strings.TrimPrefix(rawText, "```")
	rawText = strings.TrimSuffix(rawText, "```")
	rawText = strings.TrimSpace(rawText)

	var ev progressEvaluation
	if err := json.Unmarshal([]byte(rawText), &ev); err != nil {
		log.Printf("[GoalManager] Error parsing evaluation JSON: %v. Raw text was: %s", err, rawText)
		return false, "", fmt.Errorf("malformed JSON returned by evaluator: %w", err)
	}

	return ev.Achieved, ev.Reasoning, nil
}

type goalStreamHandler struct {
	mu           sync.Mutex
	sessionStore *sessions.Store
	sessionID    string
	baseMessages []json.RawMessage
	currentTurn  []sessions.RawMsg
	inAssistant  bool
}

func (h *goalStreamHandler) OnThinking(delta string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.inAssistant || len(h.currentTurn) == 0 {
		h.currentTurn = append(h.currentTurn, sessions.RawMsg{Role: "assistant", Timestamp: time.Now().Format(time.RFC3339)})
		h.inAssistant = true
	}
	idx := len(h.currentTurn) - 1
	h.currentTurn[idx].Thinking += delta
	h.syncSession()
}

func (h *goalStreamHandler) OnContent(delta string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.inAssistant || len(h.currentTurn) == 0 {
		h.currentTurn = append(h.currentTurn, sessions.RawMsg{Role: "assistant", Timestamp: time.Now().Format(time.RFC3339)})
		h.inAssistant = true
	}
	idx := len(h.currentTurn) - 1
	h.currentTurn[idx].Content += delta
	h.syncSession()
}

func (h *goalStreamHandler) OnToolCall(call ollama.ToolCall) {
	// Reconstructed tool starts handles this dynamically
}

func (h *goalStreamHandler) OnToolStart(name string, args any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.inAssistant || len(h.currentTurn) == 0 {
		h.currentTurn = append(h.currentTurn, sessions.RawMsg{Role: "assistant", Timestamp: time.Now().Format(time.RFC3339)})
		h.inAssistant = true
	}
	idx := len(h.currentTurn) - 1
	tc := ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: name,
		},
	}
	if argsBytes, err := json.Marshal(args); err == nil {
		tc.Function.Arguments = argsBytes
	}
	tcRaw, _ := json.Marshal(tc)
	h.currentTurn[idx].ToolCalls = append(h.currentTurn[idx].ToolCalls, tcRaw)
	h.syncSession()
}

func (h *goalStreamHandler) OnToolResult(name string, result string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inAssistant = false
	h.currentTurn = append(h.currentTurn, sessions.RawMsg{
		Role:      "tool",
		Name:      name,
		Content:   result,
		Timestamp: time.Now().Format(time.RFC3339),
	})
	h.syncSession()
}

func (h *goalStreamHandler) OnMediaPreProcessing(content string) {}
func (h *goalStreamHandler) OnDone(resp ollama.ChatResponse)     {}

func (h *goalStreamHandler) syncSession() {
	sess, err := h.sessionStore.Get(h.sessionID)
	if err != nil {
		return
	}

	var newRawMessages []json.RawMessage
	newRawMessages = append(newRawMessages, h.baseMessages...)
	for _, m := range h.currentTurn {
		raw, err := json.Marshal(m)
		if err == nil {
			newRawMessages = append(newRawMessages, raw)
		}
	}
	sess.Messages = newRawMessages
	_ = h.sessionStore.Save(sess)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
