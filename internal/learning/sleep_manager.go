package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type LearningState struct {
	AnalyzedSessions []string  `json:"analyzed_sessions"`
	LastResumeTime   time.Time `json:"last_resume_time"`
	StateVersion     int       `json:"state_version"`
}

const (
	defaultCycleCooldown = 15 * time.Minute // minimum rest between completed learning cycles
	defaultDeferBackoff  = 3 * time.Minute  // wait time when VRAM/hardware or background slot is busy
	maxSessionFailures   = 3                // max retries before skipping a failing session
)

type Subtask struct {
	Type     string `json:"type"`      // "analyze_session"
	TargetID string `json:"target_id"` // Session ID
}

type SleepManager struct {
	mu              sync.RWMutex
	cfgMgr          *config.Manager
	client          *ollama.Client
	sessionStore    *sessions.Store
	memoryStore     *memory.Store
	lastActivity    time.Time
	isSleeping      bool
	isLearning      bool
	learnCancel     context.CancelFunc
	state           LearningState
	statePath       string
	taskQueue       []Subtask
	inFlight        []string          // sessions currently being analyzed (requeued on pause)
	pendingWork     bool              // non-subagent mode: whether unanalyzed sessions remain
	resumeNotBefore time.Time         // don't re-enter sleep before this (resume delay)
	cooldownUntil   time.Time         // don't start next learning cycle before this time
	failedAttempts  map[string]int    // track failures per session to prevent infinite retry loops
}

func (sm *SleepManager) config() config.Config {
	return sm.cfgMgr.Get()
}

func (sm *SleepManager) setCooldown(d time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cooldownUntil = time.Now().Add(d)
}

func NewSleepManager(cfg *config.Manager, client *ollama.Client, memoryStore *memory.Store) *SleepManager {
	return &SleepManager{
		cfgMgr:         cfg,
		client:         client,
		sessionStore:   sessions.NewStore(cfg.Get().SessionsPath),
		memoryStore:    memoryStore,
		lastActivity:   time.Now(),
		statePath:      filepath.Join(cfg.Get().SessionsPath, "learning_state.json"),
		failedAttempts: make(map[string]int),
	}
}

func (sm *SleepManager) NotifyUserActivity() {
	resumeDelay := time.Duration(0)
	if d, err := time.ParseDuration(sm.config().SleepModeResumeDelay); err == nil && d > 0 {
		resumeDelay = d
	}

	sm.mu.Lock()
	sm.lastActivity = time.Now()
	if resumeDelay > 0 {
		sm.resumeNotBefore = sm.lastActivity.Add(resumeDelay)
	} else {
		sm.resumeNotBefore = sm.lastActivity
	}
	wasSleeping := sm.isSleeping
	wasLearning := sm.isLearning
	sm.mu.Unlock()

	if wasSleeping || wasLearning {
		log.Println("[sleep] User activity detected! Pausing background learning...")
		sm.Pause()
	}
}

func (sm *SleepManager) Start(ctx context.Context) {
	enabled := sm.config().SleepModeEnabled

	if !enabled {
		log.Println("[sleep] Sleep Mode is disabled in config")
		return
	}

	sm.LoadState()

	log.Println("[sleep] Sleep manager background service starting...")

	inactivityThreshold := 30 * time.Minute
	threshStr := sm.config().SleepModeInactivityThreshold
	if dur, err := time.ParseDuration(threshStr); err == nil {
		inactivityThreshold = dur
	}

	ticker := time.NewTicker(10 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				sm.Pause()
				return
			case <-ticker.C:
				sm.mu.RLock()
				lastAct := sm.lastActivity
				resumeNotBefore := sm.resumeNotBefore
				cooldownUntil := sm.cooldownUntil
				isSleeping := sm.isSleeping
				isLearning := sm.isLearning
				sm.mu.RUnlock()

				now := time.Now()
				if now.Sub(lastAct) >= inactivityThreshold && !now.Before(resumeNotBefore) && !now.Before(cooldownUntil) {
					if !isSleeping && !isLearning {
						sm.mu.Lock()
						sm.isSleeping = true
						sm.pendingWork = true
						subagentsEnabled := sm.config().SleepModeSubagentsEnabled
						var queue []Subtask
						if subagentsEnabled {
							sessList, err := sm.sessionStore.List()
							if err == nil {
								analyzed := make(map[string]bool)
								for _, id := range sm.state.AnalyzedSessions {
									analyzed[id] = true
								}
								for _, s := range sessList {
									if !analyzed[s.ID] {
										queue = append(queue, Subtask{
											Type:     "analyze_session",
											TargetID: s.ID,
										})
									}
								}
							}
						}
						sm.taskQueue = queue
						sm.mu.Unlock()

						log.Printf("[sleep] System has been idle for %v. Activating sleep mode learning (queued subtasks: %d)...", now.Sub(lastAct), len(queue))

						if subagentsEnabled {
							sm.processNextQueuedTask(ctx)
						} else {
							go sm.runLearningCycle(ctx)
						}
					} else if isSleeping && !isLearning {
						sm.mu.Lock()
						subagentsEnabled := sm.config().SleepModeSubagentsEnabled
						queueLen := len(sm.taskQueue)
						pendingWork := sm.pendingWork
						sm.mu.Unlock()

						if subagentsEnabled && queueLen > 0 {
							log.Printf("[sleep] Processing next subagent task sequentially (remaining in queue: %d)...", queueLen)
							sm.processNextQueuedTask(ctx)
						} else if !subagentsEnabled && pendingWork {
							// Non-subagent mode: retry a previously deferred cycle
							// (e.g. VRAM busy) so pending sessions are not dropped.
							go sm.runLearningCycle(ctx)
						}
					}
				}
			}
		}
	}()
}

func (sm *SleepManager) Pause() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.learnCancel != nil {
		sm.learnCancel()
		sm.learnCancel = nil
	}

	// Preserve pending learning work: requeue any in-flight sessions so the
	// analysis is resumed later instead of being dropped when the user speaks.
	if len(sm.inFlight) > 0 {
		prepend := make([]Subtask, 0, len(sm.inFlight))
		for _, id := range sm.inFlight {
			prepend = append(prepend, Subtask{Type: "analyze_session", TargetID: id})
		}
		sm.taskQueue = append(prepend, sm.taskQueue...)
		sm.inFlight = nil
	}
	sm.isLearning = false
	sm.isSleeping = false
	sm.pendingWork = false
	sm.cooldownUntil = time.Time{}
	// NOTE: taskQueue is intentionally preserved so pending subtasks survive
	// the pause and are resumed on the next sleep cycle.

	if sm.client != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = sm.client.UnloadInactiveModels(ctx, sm.config().OllamaDefaultModel)
		}()
	}
}

func normalizeModelName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if lastSlash := strings.LastIndex(name, "/"); lastSlash != -1 {
		name = name[lastSlash+1:]
	}
	name = strings.TrimSuffix(name, ":latest")
	return name
}

func (sm *SleepManager) checkHardwareAndSelectModel(ctx context.Context) (string, error) {
	subagentModel := sm.config().OllamaModelSubagent
	learningModel := sm.config().OllamaModelLearning
	defaultModel := sm.config().OllamaDefaultModel
	var primaryModel string
	if sm.config().SleepModeSubagentsEnabled && subagentModel != "" {
		primaryModel = subagentModel
	} else if learningModel != "" {
		primaryModel = learningModel
	} else if subagentModel != "" {
		primaryModel = subagentModel
	} else {
		primaryModel = defaultModel
	}

	if primaryModel == "" {
		return "", fmt.Errorf("no default, learning or subagent model configured in ollamabot config")
	}

	if sm.client == nil {
		return primaryModel, nil
	}

	ps, err := sm.client.Ps(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to query Ollama loaded models: %w", err)
	}

	if len(ps.Models) == 0 {
		return primaryModel, nil
	}

	modelIsLoaded := func(candidate string) bool {
		if candidate == "" {
			return false
		}
		candNorm := normalizeModelName(candidate)
		for _, m := range ps.Models {
			if normalizeModelName(m.Name) == candNorm {
				return true
			}
		}
		return false
	}

	if sm.config().SleepModeSubagentsEnabled && modelIsLoaded(subagentModel) {
		return subagentModel, nil
	}
	if modelIsLoaded(learningModel) {
		return learningModel, nil
	}
	if modelIsLoaded(subagentModel) {
		return subagentModel, nil
	}
	if modelIsLoaded(defaultModel) {
		return defaultModel, nil
	}

	var runningModelNames []string
	for _, m := range ps.Models {
		runningModelNames = append(runningModelNames, m.Name)
	}
	return "", fmt.Errorf("Ollama has other model(s) loaded (%s) and our models are not in memory; deferring to prevent VRAM swapping", strings.Join(runningModelNames, ", "))
}

func (sm *SleepManager) processNextQueuedTask(ctx context.Context) {
	sm.mu.Lock()
	if len(sm.taskQueue) == 0 {
		sm.mu.Unlock()
		sm.setCooldown(defaultCycleCooldown)
		return
	}
	sm.mu.Unlock()

	modelToUse, err := sm.checkHardwareAndSelectModel(ctx)
	if err != nil {
		log.Printf("[sleep] Hardware check / model selection deferred: %v. Retrying in %v...", err, defaultDeferBackoff)
		sm.setCooldown(defaultDeferBackoff)
		return
	}

	sm.mu.Lock()
	if len(sm.taskQueue) == 0 {
		sm.mu.Unlock()
		return
	}

	// Collect up to 5 analyze_session tasks to consolidate
	var sessions []string
	var remainingQueue []Subtask
	for _, task := range sm.taskQueue {
		if len(sessions) < 5 && task.Type == "analyze_session" {
			sessions = append(sessions, task.TargetID)
		} else {
			remainingQueue = append(remainingQueue, task)
		}
	}
	sm.taskQueue = remainingQueue
	sm.mu.Unlock()

	if len(sessions) > 0 {
		go sm.runLearningCycleForSessionsWithModel(ctx, sessions, modelToUse)
	}
}

// requeueSessions puts session IDs back at the front of the task queue so they
// are retried on a later tick. It is used when a learning cycle cannot start
// (already learning, or the shared background slot is busy), preventing queued
// sessions from being silently dropped.
func (sm *SleepManager) requeueSessions(ids []string) {
	if len(ids) == 0 {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	prepend := make([]Subtask, 0, len(ids))
	for _, id := range ids {
		prepend = append(prepend, Subtask{Type: "analyze_session", TargetID: id})
	}
	sm.taskQueue = append(prepend, sm.taskQueue...)
}

func (sm *SleepManager) LoadState() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	data, err := os.ReadFile(sm.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			sm.state = LearningState{
				AnalyzedSessions: []string{},
				StateVersion:     1,
			}
			return
		}
		log.Printf("[sleep] Error reading state file: %v", err)
		return
	}

	if err := json.Unmarshal(data, &sm.state); err != nil {
		log.Printf("[sleep] Error unmarshaling state: %v", err)
	}
}

func (sm *SleepManager) SaveState() error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	data, err := json.MarshalIndent(sm.state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sm.statePath, data, 0644)
}

type sleepStreamHandler struct{}

func (d *sleepStreamHandler) OnThinking(delta string)                                            {}
func (d *sleepStreamHandler) OnContent(delta string)                                             {}
func (d *sleepStreamHandler) OnToolCall(call ollama.ToolCall)                                    {}
func (d *sleepStreamHandler) OnToolStart(name string, args any, source string)                   {}
func (d *sleepStreamHandler) OnToolResult(name string, result string, source string)             {}
func (d *sleepStreamHandler) OnMediaPreProcessing(content string)                                {}
func (d *sleepStreamHandler) OnDone(resp ollama.ChatResponse)                                    {}
func (d *sleepStreamHandler) OnEvent(kind string, data any)                                      {}
func (d *sleepStreamHandler) OnContextOptimizationStart(tokensBefore int, percentBefore float64) {}
func (d *sleepStreamHandler) OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64) {
}
func (d *sleepStreamHandler) OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int) {
}

func (sm *SleepManager) runLearningCycle(parentCtx context.Context) {
	modelToUse, err := sm.checkHardwareAndSelectModel(parentCtx)
	if err != nil {
		log.Printf("[sleep] Hardware check / model selection deferred for learning cycle: %v. Retrying in %v...", err, defaultDeferBackoff)
		sm.setCooldown(defaultDeferBackoff)
		return
	}

	sessList, err := sm.sessionStore.List()
	if err != nil {
		log.Printf("[sleep] Error listing sessions: %v", err)
		sm.setCooldown(defaultDeferBackoff)
		return
	}

	var sessionsToAnalyze []string
	sm.mu.RLock()
	analyzed := make(map[string]bool)
	for _, id := range sm.state.AnalyzedSessions {
		analyzed[id] = true
	}
	sm.mu.RUnlock()

	for _, s := range sessList {
		if !analyzed[s.ID] {
			sessionsToAnalyze = append(sessionsToAnalyze, s.ID)
			if len(sessionsToAnalyze) >= 5 {
				break
			}
		}
	}

	if len(sessionsToAnalyze) == 0 {
		sm.mu.Lock()
		sm.pendingWork = false
		sm.mu.Unlock()
		sm.setCooldown(defaultCycleCooldown)
		log.Println("[sleep] No new sessions to analyze.")
		return
	}

	sm.runLearningCycleForSessionsWithModel(parentCtx, sessionsToAnalyze, modelToUse)
}

func (sm *SleepManager) runLearningCycleForSessionsWithModel(parentCtx context.Context, sessionsToAnalyze []string, modelToUse string) {
	if len(sessionsToAnalyze) == 0 {
		return
	}
	sm.mu.Lock()
	if sm.isLearning {
		sm.mu.Unlock()
		sm.requeueSessions(sessionsToAnalyze)
		return
	}
	sm.isLearning = true
	sm.inFlight = append([]string(nil), sessionsToAnalyze...)
	ctx, cancel := context.WithCancel(parentCtx)
	sm.learnCancel = cancel
	sm.mu.Unlock()

	releaseSlot := sessions.TryAcquireBackgroundSlot()
	if releaseSlot == nil {
		sm.mu.Lock()
		sm.isLearning = false
		sm.inFlight = nil
		if sm.learnCancel != nil {
			sm.learnCancel = nil
		}
		sm.mu.Unlock()
		cancel()
		// Put the sessions back so they are not silently dropped: the global
		// background slot is shared with GoalManager/PlanMonitor and may be
		// busy when this runs.
		sm.requeueSessions(sessionsToAnalyze)
		sm.setCooldown(defaultDeferBackoff)
		log.Printf("[sleep] Background slot busy, deferring learning cycle for %v", defaultDeferBackoff)
		return
	}
	defer releaseSlot()

	defer func() {
		cancel()
		sm.mu.Lock()
		sm.isLearning = false
		sm.inFlight = nil
		if sm.learnCancel != nil {
			sm.learnCancel = nil
		}
		sm.mu.Unlock()
	}()

	log.Printf("[sleep] Continuous learning cycle started for %d sessions: %v.", len(sessionsToAnalyze), sessionsToAnalyze)

	var historyText strings.Builder
	var feedbackText strings.Builder
	var validSessions []string

	for _, sessionID := range sessionsToAnalyze {
		sess, err := sm.sessionStore.Get(sessionID)
		if err != nil {
			log.Printf("[sleep] Error loading session %s: %v", sessionID, err)
			continue
		}
		if len(sess.Messages) == 0 {
			sm.mu.Lock()
			sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, sessionID)
			sm.mu.Unlock()
			_ = sm.SaveState()
			continue
		}

		userMsgCount := 0
		for _, raw := range sess.Messages {
			var m struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal(raw, &m); err == nil && m.Role == "user" {
				userMsgCount++
			}
		}

		// Skip trivial sessions: if there are fewer than 2 user messages and no explicit feedback,
		// there is nothing meaningful to extract for reflection or skill refinement.
		if userMsgCount < 2 && len(sess.Feedback) == 0 {
			sm.mu.Lock()
			sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, sessionID)
			sm.mu.Unlock()
			_ = sm.SaveState()
			log.Printf("[sleep] Session %s marked analyzed (trivial, <2 user turns and no feedback)", sessionID)
			continue
		}

		validSessions = append(validSessions, sessionID)

		fmt.Fprintf(&historyText, "\n--- SESSION: %s (ID: %s) ---\n", sess.Title, sess.ID)
		for idx, raw := range sess.Messages {
			var m struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw, &m); err == nil {
				fmt.Fprintf(&historyText, "[Msg %d] %s: %s\n", idx+1, m.Role, m.Content)
			}
		}

		if len(sess.Feedback) > 0 {
			fmt.Fprintf(&feedbackText, "\n--- FEEDBACK FOR SESSION: %s (ID: %s) ---\n", sess.Title, sess.ID)
			for _, fb := range sess.Feedback {
				emoji := "👍"
				if fb.Reaction == "negative" {
					emoji = "👎"
				}
				fmt.Fprintf(&feedbackText, "- Message #%d: %s %s\n", fb.MessageIndex+1, emoji, fb.Reaction)
			}
		}
	}

	if len(validSessions) == 0 {
		log.Println("[sleep] No valid sessions to analyze after filtering.")
		sm.setCooldown(defaultCycleCooldown)
		return
	}

	learningModel := modelToUse
	if learningModel == "" {
		learningModel = config.ResolveModel(sm.config(), config.ModelRoleLearning)
	}
	if learningModel == "" {
		log.Println("[sleep] No default or learning model configured. Aborting cycle.")
		sm.setCooldown(defaultCycleCooldown)
		return
	}

	// Verify the learning model supports tool calls. The reflector agent
	// relies on tools to manage skills and user profile; without tool
	// support it loops uselessly until MaxIterations.
	probePath := cache.DefaultPath()
	if !cache.SupportsCapability(probePath, learningModel, "tools") {
		// Model not in cache or lacks tools — run a lightweight probe.
		if sm.client != nil {
			probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
			runner := probe.NewRunner(sm.client)
			result, err := runner.Tools(probeCtx, learningModel)
			probeCancel()
			if err != nil || result.Status != capabilities.Confirmed {
				log.Printf("[sleep] Learning model %q does not support tools, skipping reflection cycle", learningModel)
				sm.setCooldown(defaultDeferBackoff)
				return
			}
			_ = cache.SaveProbeRun(probePath, cache.ProbeRun{
				Name:    "tools",
				Model:   learningModel,
				Status:  result.Status,
				Details: result.Details,
				RunAt:   time.Now(),
			})
		} else {
			log.Printf("[sleep] Learning model %q does not support tools, skipping reflection cycle", learningModel)
			sm.setCooldown(defaultDeferBackoff)
			return
		}
	}

	// Load global text feedback submitted by the user.
	textFeedbackEntries, fbErr := LoadFeedback(sm.config().SessionsPath)
	var textFeedbackSection string
	if fbErr != nil {
		log.Printf("[sleep] Error loading text feedback: %v", fbErr)
	} else if len(textFeedbackEntries) > 0 {
		var tfb strings.Builder
		tfb.WriteString("\n--- USER TEXT FEEDBACK ---\n")
		for _, e := range textFeedbackEntries {
			fmt.Fprintf(&tfb, "- [%s] %s\n", e.Category, e.Text)
		}
		textFeedbackSection = tfb.String()
	}

	analysisPrompt := fmt.Sprintf(`You are the OllamaBot self-refining learning analyzer.
Analyze the following conversation histories from %d consolidated sessions:
---
CONVERSATION HISTORIES:
%s
%s%s---

Your instructions:
1. Pay highest priority to explicit user text feedback or negative feedback (👎). If the user corrected the assistant, determine what rule or guideline would prevent the mistake and update or create ONLY the relevant skill.
2. If there are NO user complaints, NO mistakes, and NO durable facts to remember, DO NOT call any tools. Simply write a concise final summary.
3. NEVER edit unrelated skills (e.g. do not add conversational guidelines or unit conversions to coding/refactor skills).
4. Do NOT write to audit_log.md — the system logs your actions automatically.
Provide a clear final summary of your reflection.`, len(validSessions), historyText.String(), feedbackText.String(), textFeedbackSection)

	registry := tools.NewRegistry(false, sm.config().Workspace, sm.memoryStore, sm.client, sm.config().OllamaModelEmbed, tools.SearchConfig{})
	registry.SetSkillsPath(sm.config().SkillsPath)
	registry.SetSessionStore(sm.sessionStore)

	reflectorAgent := agent.NewAgent(sm.cfgMgr, sm.client, registry)
	reflectorAgent.SetMaxIterations(6)
	reflectorAgent.SetOptions(map[string]any{
		"temperature": 0.1,
		"num_predict": 2048,
	})

	systemPrompt := `You are the OllamaBot Self-Improvement Reflector.
You operate in the background during sleep mode.
You have access to tools to manage skills ('skill_list', 'skill_get', 'skill_create', 'skill_edit', 'skill_delete'), update user profile ('agent/USER_PROFILE.md'), and manage long-term memory ('memory_add', 'memory_delete', 'memory_list').

BE CONSERVATIVE AND HIGHLY SELECTIVE:
- Do NOT modify existing skills or create new ones for normal, successful, or routine conversations.
- ONLY create or edit a skill if there was an explicit user correction, negative feedback (👎), or a clear repeated mistake.
- NEVER modify an unrelated skill (e.g. NEVER add conversational guidelines, unit conversions, or weather notes to a coding/refactoring skill).
- Keep user profile ('agent/USER_PROFILE.md') limited to durable, explicit facts about the user (e.g. name, declared language preferences, explicit coding style preferences). Do NOT record temporary discussion topics (e.g. asking about today's weather) as user preferences.
- Only call 'memory_add' for durable facts, key decisions, or technical solutions that will be useful across sessions.
- Do NOT attempt to edit or write to 'skills/audit_log.md' — the system automatically logs your reflection summary and actions.
- If everything went well and no changes are required, DO NOT call any tools. Simply output a brief summary explaining that the session was successful and no modifications were needed.`

	messages := []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: analysisPrompt},
	}

	runCtx, runCancel := agent.SubagentContext(ctx, sm.config())
	finalHistory, err := reflectorAgent.Run(runCtx, learningModel, messages, false, &sleepStreamHandler{})
	runCancel()
	if err != nil {
		sm.mu.Lock()
		for _, id := range validSessions {
			sm.failedAttempts[id]++
			if sm.failedAttempts[id] >= maxSessionFailures {
				log.Printf("[sleep] Session %s failed %d times in reflection, marking analyzed to avoid infinite retry loop", id, sm.failedAttempts[id])
				sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, id)
			}
		}
		sm.mu.Unlock()
		_ = sm.SaveState()
		sm.setCooldown(defaultDeferBackoff)

		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[sleep] Reflection agent timed out after %d minutes", sm.config().SubagentTimeoutMinutes)
			return
		}
		log.Printf("[sleep] Reflection agent run encountered error (likely paused/canceled): %v", err)
		return
	}

	var actions []string
	var summary string
	for _, msg := range finalHistory {
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) != "" {
			summary = msg.Content
		}
		for _, tc := range msg.ToolCalls {
			if strings.HasPrefix(tc.Function.Name, "skill_") || strings.HasPrefix(tc.Function.Name, "memory_") || tc.Function.Name == "write_file" || tc.Function.Name == "edit_file" {
				actions = append(actions, fmt.Sprintf("Called tool %s with args %s", tc.Function.Name, string(tc.Function.Arguments)))
			}
		}
	}

	// Automatic long-term memory consolidation & pruning
	if sm.memoryStore != nil {
		report, consErr := sm.memoryStore.ConsolidateAndPrune(0.82)
		if consErr != nil {
			log.Printf("[sleep] Memory consolidation error: %v", consErr)
		} else if report.MergedCount > 0 || report.PrunedCount > 0 {
			log.Printf("[sleep] Long-term memory consolidated: %d duplicates merged, %d pruned, %d remaining.", report.MergedCount, report.PrunedCount, report.RemainingCount)
			actions = append(actions, fmt.Sprintf("Consolidated memory store: merged %d duplicates, pruned %d entries (%d active)", report.MergedCount, report.PrunedCount, report.RemainingCount))
		}
	}

	// Clear processed text feedback entries.
	if len(textFeedbackEntries) > 0 {
		if err := ClearFeedback(sm.config().SessionsPath); err != nil {
			log.Printf("[sleep] Error clearing text feedback: %v", err)
		}
	}

	consolidatedSessIDs := strings.Join(validSessions, ", ")
	if len(actions) > 0 {
		sm.appendToAuditLog(consolidatedSessIDs, actions, summary)
	}

	// Auto-name untitled sessions and update daily digest
	for _, sessID := range validSessions {
		if sess, err := sm.sessionStore.Get(sessID); err == nil {
			if sessions.IsDefaultTitle(sess.Title) {
				var text string
				for _, raw := range sess.Messages {
					var m struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					}
					if json.Unmarshal(raw, &m) == nil && m.Role == "user" && strings.TrimSpace(m.Content) != "" {
						text = m.Content
						break
					}
				}
				if text != "" {
					_ = engine.AutoNameSession(ctx, sm.config(), sm.client, sm.sessionStore, sessID, text)
				}
			}
		}
	}

	sm.generateDailyDigest(validSessions, summary)

	sm.mu.Lock()
	for _, sID := range validSessions {
		sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, sID)
		delete(sm.failedAttempts, sID)
	}
	sm.mu.Unlock()
	_ = sm.SaveState()

	log.Printf("[sleep] Analysis of sessions [%s] completed successfully.", consolidatedSessIDs)
	sm.setCooldown(defaultCycleCooldown)

	// Unload inactive models so Ollama frees VRAM and GPU can enter idle power state
	if sm.client != nil {
		go func() {
			unloadCtx, unloadCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer unloadCancel()
			_ = sm.client.UnloadInactiveModels(unloadCtx, sm.config().OllamaDefaultModel)
		}()
	}
}

func (sm *SleepManager) generateDailyDigest(analyzedIDs []string, reflectionSummary string) {
	if len(analyzedIDs) == 0 {
		return
	}
	digestsDir := filepath.Join(sm.config().SessionsPath, "digests")
	if err := os.MkdirAll(digestsDir, 0755); err != nil {
		log.Printf("[sleep] Failed to create digests dir: %v", err)
		return
	}

	todayStr := time.Now().Format("2006-01-02")
	digestPath := filepath.Join(digestsDir, fmt.Sprintf("digest_%s.md", todayStr))

	var sb strings.Builder
	if _, err := os.Stat(digestPath); os.IsNotExist(err) {
		fmt.Fprintf(&sb, "# Daily Digest — %s\n\n", todayStr)
	} else {
		sb.WriteString("\n---\n\n")
	}

	fmt.Fprintf(&sb, "## Sleep Reflection Run (%s)\n", time.Now().Format("15:04:05"))
	fmt.Fprintf(&sb, "- Analyzed Sessions: %d (%s)\n", len(analyzedIDs), strings.Join(analyzedIDs, ", "))
	for _, id := range analyzedIDs {
		if sess, err := sm.sessionStore.Get(id); err == nil {
			title := sess.Title
			if title == "" {
				title = "(Untitled)"
			}
			fmt.Fprintf(&sb, "  - Session %s: %q (Model: %s)\n", sess.ID, title, sess.Model)
			if sess.GoalObjective != "" {
				fmt.Fprintf(&sb, "    - Goal Objective: %s [Status: %s]\n", sess.GoalObjective, sess.GoalStatus)
			}
		}
	}
	if reflectionSummary != "" {
		fmt.Fprintf(&sb, "- Reflection Summary:\n%s\n", reflectionSummary)
	}

	f, err := os.OpenFile(digestPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[sleep] Failed to open daily digest file: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(sb.String())
	log.Printf("[sleep] Updated daily digest at %s", digestPath)
}

func (sm *SleepManager) appendToAuditLog(sessionID string, actions []string, summary string) {
	auditPath := filepath.Join(sm.config().SkillsPath, "audit_log.md")

	if _, err := os.Stat(auditPath); os.IsNotExist(err) {
		initialHeader := "# Skills Continuous Learning Audit Log\n\nThis file tracks all autonomous updates, creations, and deletions of skills or settings.\n\n"
		_ = os.WriteFile(auditPath, []byte(initialHeader), 0644)
	}

	var entry strings.Builder
	entry.WriteString(fmt.Sprintf("## Audit Entry: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	entry.WriteString(fmt.Sprintf("- **Analyzed Session ID**: %s\n", sessionID))
	entry.WriteString("- **Actions Executed**:\n")
	for _, act := range actions {
		entry.WriteString(fmt.Sprintf("  - %s\n", act))
	}
	entry.WriteString(fmt.Sprintf("- **Reflection Summary**:\n\n%s\n\n---\n\n", summary))

	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[sleep] Failed to open audit log: %v", err)
		return
	}
	defer f.Close()

	if _, err := f.WriteString(entry.String()); err != nil {
		log.Printf("[sleep] Failed to write to audit log: %v", err)
	}
}
