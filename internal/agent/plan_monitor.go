package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

const defaultPlanStaleAfter = 10 * time.Minute

type PlanNotificationFunc func(sessionID string, plan sessions.SessionPlan, message string)

type PlanMonitor struct {
	mu           sync.RWMutex
	cfg          config.Config
	client       *ollama.Client
	sessionStore *sessions.Store
	memoryStore  *memory.Store
	isWorking    map[string]bool
	cancelFunc   context.CancelFunc
	tickerDone   chan struct{}
	interval     time.Duration
	staleAfter   time.Duration
	notifiers    map[string]PlanNotificationFunc
}

func NewPlanMonitor(cfg config.Config, client *ollama.Client, memoryStore *memory.Store) *PlanMonitor {
	if memoryStore == nil {
		memoryStore = memory.NewStore(cfg.MemoryPath)
	}
	return &PlanMonitor{
		cfg:          cfg,
		client:       client,
		sessionStore: sessions.NewStore(cfg.SessionsPath),
		memoryStore:  memoryStore,
		isWorking:    make(map[string]bool),
		interval:     2 * time.Minute,
		staleAfter:   defaultPlanStaleAfter,
		notifiers:    make(map[string]PlanNotificationFunc),
	}
}

func (pm *PlanMonitor) Start(ctx context.Context) {
	pm.mu.Lock()
	if pm.cancelFunc != nil {
		pm.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	pm.cancelFunc = cancel
	pm.tickerDone = make(chan struct{})
	interval := pm.interval
	pm.mu.Unlock()

	go pm.ResumeActivePlans(ctx)

	ticker := time.NewTicker(interval)
	go func() {
		defer close(pm.tickerDone)
		log.Println("[PlanMonitor] Background monitor started")
		for {
			select {
			case <-ticker.C:
				pm.Tick(ctx)
			case <-ctx.Done():
				ticker.Stop()
				log.Println("[PlanMonitor] Background monitor stopped")
				return
			}
		}
	}()
}

func (pm *PlanMonitor) Stop() {
	pm.mu.Lock()
	if pm.cancelFunc == nil {
		pm.mu.Unlock()
		return
	}
	cancel := pm.cancelFunc
	done := pm.tickerDone
	pm.cancelFunc = nil
	pm.tickerDone = nil
	pm.mu.Unlock()

	cancel()
	if done != nil {
		<-done
	}
}

func (pm *PlanMonitor) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.interval = d
}

func (pm *PlanMonitor) RegisterNotifier(sessionID string, notifier PlanNotificationFunc) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || notifier == nil {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.notifiers[sessionID] = notifier
}

func (pm *PlanMonitor) UnregisterNotifier(sessionID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.notifiers, sessionID)
}

func (pm *PlanMonitor) notify(sessionID string, plan sessions.SessionPlan, message string) {
	pm.mu.RLock()
	notifier := pm.notifiers[sessionID]
	pm.mu.RUnlock()
	if notifier != nil {
		go notifier(sessionID, plan, message)
	}
}

func (pm *PlanMonitor) ResumeActivePlans(ctx context.Context) {
	pm.Tick(ctx)
}

func (pm *PlanMonitor) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	sessList, err := pm.sessionStore.List()
	if err != nil {
		log.Printf("[PlanMonitor] list sessions: %v", err)
		return
	}
	now := time.Now()
	for _, meta := range sessList {
		plan := meta.ActivePlan
		if plan == nil || plan.Completed >= len(plan.Steps) {
			continue
		}
		reason := ""
		switch plan.Status {
		case sessions.PlanStatusDeferred:
			if plan.DeferredUntil == nil || plan.DeferredUntil.After(now) {
				continue
			}
			reason = "scheduled deferred plan is due"
		case sessions.PlanStatusActive:
			lastProgress := plan.LastProgressAt
			if lastProgress.IsZero() {
				lastProgress = meta.UpdatedAt
			}
			if !lastProgress.IsZero() && now.Sub(lastProgress) < pm.staleAfter {
				continue
			}
			reason = "active plan appears stalled"
		default:
			continue
		}
		if pm.tryStart(meta.ID) {
			go pm.resumePlan(ctx, meta.ID, reason)
			return
		}
	}
}

func (pm *PlanMonitor) tryStart(sessionID string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.isWorking[sessionID] {
		return false
	}
	pm.isWorking[sessionID] = true
	return true
}

func (pm *PlanMonitor) finish(sessionID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.isWorking, sessionID)
}

func (pm *PlanMonitor) resumePlan(ctx context.Context, sessionID string, reason string) {
	defer pm.finish(sessionID)

	sess, err := pm.sessionStore.Get(sessionID)
	if err != nil {
		log.Printf("[PlanMonitor] load session %s: %v", sessionID, err)
		return
	}
	if sess.ActivePlan == nil || sess.ActivePlan.Completed >= len(sess.ActivePlan.Steps) {
		return
	}
	plan := *sess.ActivePlan
	if plan.Status == sessions.PlanStatusDeferred {
		plan, err = sessions.ResumeDeferredPlan(pm.sessionStore, sessionID)
		if err != nil {
			log.Printf("[PlanMonitor] resume deferred plan %s: %v", sessionID, err)
			return
		}
		sess, err = pm.sessionStore.Get(sessionID)
		if err != nil {
			log.Printf("[PlanMonitor] reload resumed plan %s: %v", sessionID, err)
			return
		}
	}

	current := plan.Completed + 1
	if current > len(plan.Steps) {
		current = len(plan.Steps)
	}
	message := fmt.Sprintf("Resuming approved plan step %d/%d: %s", current, len(plan.Steps), plan.Steps[current-1])
	pm.notify(sessionID, plan, message)

	resumePrompt := sessions.RawMsg{
		Role:      "system",
		Content:   fmt.Sprintf("Plan monitor resume: %s. Continue the approved plan from step %d of %d: %s. Execute tools now; do not ask for a new plan.", reason, current, len(plan.Steps), plan.Steps[current-1]),
		Timestamp: time.Now().Format(time.RFC3339),
	}
	resumeRaw, _ := json.Marshal(resumePrompt)
	sess.Messages = append(sess.Messages, resumeRaw)
	if err := pm.sessionStore.Save(sess); err != nil {
		log.Printf("[PlanMonitor] save resume message %s: %v", sessionID, err)
		return
	}

	ollamaMessages := rawMessagesToOllama(sess.Messages)
	registry := tools.NewRegistry(pm.cfg.WebSearchEnabled, pm.cfg.Workspace, pm.memoryStore, pm.client, pm.cfg.OllamaModelEmbed, tools.SearchConfig{
		Providers:    pm.cfg.SearchProviders,
		BraveAPIKey:  pm.cfg.BraveSearchAPIKey,
		TavilyAPIKey: pm.cfg.TavilyAPIKey,
	})
	registry.SetSessionStore(pm.sessionStore)
	registry.SetSessionID(sessionID)
	registry.SetSessionsPath(pm.cfg.SessionsPath)
	registry.SetPlanProgressHandler(func(id string, plan sessions.SessionPlan) {
		pm.notify(id, plan, sessions.FormatPlanProgressShort(plan))
	})

	handler := &goalStreamHandler{
		sessionStore: pm.sessionStore,
		sessionID:    sessionID,
		baseMessages: sess.Messages,
	}
	a := NewAgent(pm.cfg, pm.client, registry)
	model := config.ResolveModel(pm.cfg, config.ModelRoleMain)
	if strings.TrimSpace(model) == "" {
		model = pm.cfg.OllamaDefaultModel
	}
	finalHistory, err := a.Run(ctx, model, ollamaMessages, pm.cfg.OllamaThinkEnabled, handler)
	if err != nil {
		log.Printf("[PlanMonitor] agent run for session %s failed: %v", sessionID, err)
		pm.notify(sessionID, plan, fmt.Sprintf("Plan monitor error: %v", err))
		return
	}
	if err := saveOllamaHistory(pm.sessionStore, sessionID, finalHistory); err != nil {
		log.Printf("[PlanMonitor] save final history for %s: %v", sessionID, err)
	}
}

func rawMessagesToOllama(rawMessages []json.RawMessage) []ollama.Message {
	out := make([]ollama.Message, 0, len(rawMessages))
	for _, raw := range rawMessages {
		var m sessions.RawMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		var toolCalls []ollama.ToolCall
		for _, tcRaw := range m.ToolCalls {
			var tc ollama.ToolCall
			if err := json.Unmarshal(tcRaw, &tc); err == nil {
				toolCalls = append(toolCalls, tc)
			}
		}
		out = append(out, ollama.Message{
			Role:      m.Role,
			Content:   m.Content,
			Thinking:  m.Thinking,
			Name:      m.Name,
			ToolCalls: toolCalls,
		})
	}
	return out
}

func saveOllamaHistory(store *sessions.Store, sessionID string, history []ollama.Message) error {
	sess, err := store.Get(sessionID)
	if err != nil {
		return err
	}
	rawMessages := make([]json.RawMessage, 0, len(history))
	for _, m := range history {
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
		raw, _ := json.Marshal(rm)
		rawMessages = append(rawMessages, raw)
	}
	sess.Messages = rawMessages
	return store.Save(sess)
}
