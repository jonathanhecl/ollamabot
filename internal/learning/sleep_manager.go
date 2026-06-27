package learning

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

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type LearningState struct {
	AnalyzedSessions []string  `json:"analyzed_sessions"`
	LastResumeTime   time.Time `json:"last_resume_time"`
	StateVersion     int       `json:"state_version"`
}

type Subtask struct {
	Type     string `json:"type"`      // "analyze_session"
	TargetID string `json:"target_id"` // Session ID
}

type SleepManager struct {
	mu           sync.RWMutex
	cfgMgr       *config.Manager
	client       *ollama.Client
	sessionStore *sessions.Store
	memoryStore  *memory.Store
	lastActivity time.Time
	isSleeping   bool
	isLearning   bool
	learnCancel  context.CancelFunc
	state        LearningState
	statePath    string
	taskQueue    []Subtask
}

func (sm *SleepManager) config() config.Config {
	return sm.cfgMgr.Get()
}

func NewSleepManager(cfg *config.Manager, client *ollama.Client, memoryStore *memory.Store) *SleepManager {
	return &SleepManager{
		cfgMgr:       cfg,
		client:       client,
		sessionStore: sessions.NewStore(cfg.Get().SessionsPath),
		memoryStore:  memoryStore,
		lastActivity: time.Now(),
		statePath:    filepath.Join(cfg.Get().SessionsPath, "learning_state.json"),
	}
}

func (sm *SleepManager) NotifyUserActivity() {
	sm.mu.Lock()
	sm.lastActivity = time.Now()
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
				isSleeping := sm.isSleeping
				isLearning := sm.isLearning
				sm.mu.RUnlock()

				now := time.Now()
				if now.Sub(lastAct) >= inactivityThreshold {
					if !isSleeping && !isLearning {
						sm.mu.Lock()
						sm.isSleeping = true
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
						sm.mu.Unlock()

						if subagentsEnabled && queueLen > 0 {
							log.Printf("[sleep] Processing next subagent task sequentially (remaining in queue: %d)...", queueLen)
							sm.processNextQueuedTask(ctx)
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
	sm.isLearning = false
	sm.isSleeping = false
	sm.taskQueue = nil
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
	if subagentModel != "" {
		primaryModel = subagentModel
	} else if learningModel != "" {
		primaryModel = learningModel
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

	if modelIsLoaded(subagentModel) {
		return subagentModel, nil
	}
	if modelIsLoaded(learningModel) {
		return learningModel, nil
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
		return
	}
	sm.mu.Unlock()

	modelToUse, err := sm.checkHardwareAndSelectModel(ctx)
	if err != nil {
		log.Printf("[sleep] Hardware check / model selection deferred: %v. Retrying in next ticker loop...", err)
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
func (d *sleepStreamHandler) OnToolStart(name string, args any)                                  {}
func (d *sleepStreamHandler) OnToolResult(name string, result string)                            {}
func (d *sleepStreamHandler) OnMediaPreProcessing(content string)                                {}
func (d *sleepStreamHandler) OnDone(resp ollama.ChatResponse)                                    {}
func (d *sleepStreamHandler) OnContextOptimizationStart(tokensBefore int, percentBefore float64) {}
func (d *sleepStreamHandler) OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64) {
}
func (d *sleepStreamHandler) OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int) {
}

func (sm *SleepManager) runLearningCycle(parentCtx context.Context) {
	modelToUse, err := sm.checkHardwareAndSelectModel(parentCtx)
	if err != nil {
		log.Printf("[sleep] Hardware check / model selection deferred for learning cycle: %v. Retrying in next ticker loop...", err)
		return
	}

	sessList, err := sm.sessionStore.List()
	if err != nil {
		log.Printf("[sleep] Error listing sessions: %v", err)
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
		return
	}
	sm.isLearning = true
	ctx, cancel := context.WithCancel(parentCtx)
	sm.learnCancel = cancel
	sm.mu.Unlock()

	releaseSlot := sessions.TryAcquireBackgroundSlot()
	if releaseSlot == nil {
		sm.mu.Lock()
		sm.isLearning = false
		if sm.learnCancel != nil {
			sm.learnCancel = nil
		}
		sm.mu.Unlock()
		log.Printf("[sleep] Background slot busy, deferring learning cycle")
		return
	}
	defer releaseSlot()

	defer func() {
		sm.mu.Lock()
		sm.isLearning = false
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
		return
	}

	learningModel := modelToUse
	if learningModel == "" {
		learningModel = sm.config().OllamaModelSubagent
		if learningModel == "" {
			learningModel = sm.config().OllamaModelLearning
		}
		if learningModel == "" {
			learningModel = sm.config().OllamaDefaultModel
		}
	}
	if learningModel == "" {
		log.Println("[sleep] No default, learning or subagent model configured. Aborting cycle.")
		return
	}

	analysisPrompt := fmt.Sprintf(`You are the OllamaBot self-refining learning analyzer.
Analyze the following conversation histories from %d consolidated sessions for:
- Potential errors, user frustration, repetitive mistakes, or areas where the assistant struggled.
- User preferences, coding style preferences, spoken language preferences, tastes, background info, or user name.

---
CONVERSATION HISTORIES:
%s
%s---

Your goal:
1. Pay special attention to messages marked with user feedback reactions:
   - A 👎 (negative) indicates the user was unhappy with that response — investigate what went wrong and create/modify skills to prevent the issue.
   - A 👍 (positive) confirms the approach was good — reinforce those patterns in skills if applicable.
2. Determine if the assistant made any mistakes, failed to solve a task, or caused user frustration. If so, create/modify the corresponding skills.
3. Identify user preferences or tastes. If found, update the User Profile at 'agent/USER_PROFILE.md' by reading it first with 'read_file' and updating it with 'Write' or 'Edit'.
4. Call the appropriate tools to make these improvements. You have access to:
   - 'skill_list', 'skill_get', 'skill_create', 'skill_edit', 'skill_delete' for skill management. ALWAYS run 'skill_list' first to check for existing skills with similar intent. If a similar skill exists, modify it using 'skill_edit' instead of creating a duplicate file to prevent cluttering.
   - 'read_file', 'Write', 'Edit' to update 'agent/SOUL.md' or 'agent/USER_PROFILE.md'.

If no changes are needed to skills or the user profile, respond explaining why, and do not call any tools.
Provide a clear final summary of what you did.`, len(validSessions), historyText.String(), feedbackText.String())

	registry := tools.NewRegistry(sm.config().WebSearchEnabled, sm.config().Workspace, sm.memoryStore, sm.client, sm.config().OllamaModelEmbed, tools.SearchConfig{
		Providers:    sm.config().SearchProviders,
		BraveAPIKey:  sm.config().BraveSearchAPIKey,
		TavilyAPIKey: sm.config().TavilyAPIKey,
	})
	registry.SetSkillsPath(sm.config().SkillsPath)

	reflectorAgent := agent.NewAgent(sm.cfgMgr, sm.client, registry)
	reflectorAgent.SetOptions(map[string]any{
		"temperature": 0.1,
	})

	systemPrompt := `You are the OllamaBot Self-Improvement Reflector.
You operate in the background during sleep mode.
You have access to tools to modify skills, identity, and the user profile.
Be precise and constructive. Focus on creating high-quality, actionable, clear skill guidelines.
When editing or creating skills, use standard SKILL.md format:
- Frontmatter containing name, description, homepage.
- ## Description header.
- ## Instructions header containing list items starting with - [ ], -, or numbered steps.

To prevent duplicates, you MUST check the existing skills with 'skill_list' before creating a new one. If a skill with similar intent exists, modify it instead of creating a new one.

Keep the user profile ('agent/USER_PROFILE.md') structured:
- Name
- Preferred Languages
- Coding Styles & Preferences
- Tastes & Interests
- General Context & Past Decisions

Log all updates you make in the audit log ('skills/audit_log.md'). You can write or edit this file using 'Write' or 'Edit' tools. Each log entry must include:
- Date/time
- Chat Session ID(s) analyzed
- Issue or user preferences detected
- Actions executed (skills created, user profile updated, etc.)
- Justification/Reasoning`

	messages := []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: analysisPrompt},
	}

	finalHistory, err := reflectorAgent.Run(ctx, learningModel, messages, true, &sleepStreamHandler{})
	if err != nil {
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
			if strings.HasPrefix(tc.Function.Name, "skill_") {
				actions = append(actions, fmt.Sprintf("Called tool %s with args %s", tc.Function.Name, string(tc.Function.Arguments)))
			}
		}
	}

	consolidatedSessIDs := strings.Join(validSessions, ", ")
	if len(actions) > 0 {
		sm.appendToAuditLog(consolidatedSessIDs, actions, summary)
	}

	sm.mu.Lock()
	for _, sID := range validSessions {
		sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, sID)
	}
	sm.mu.Unlock()
	_ = sm.SaveState()

	log.Printf("[sleep] Analysis of sessions [%s] completed successfully.", consolidatedSessIDs)
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
