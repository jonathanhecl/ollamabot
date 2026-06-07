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
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type LearningState struct {
	AnalyzedSessions []string  `json:"analyzed_sessions"`
	LastResumeTime   time.Time `json:"last_resume_time"`
	StateVersion     int       `json:"state_version"`
}

type SleepManager struct {
	mu           sync.RWMutex
	cfg          config.Config
	client       *ollama.Client
	sessionStore *sessions.Store
	lastActivity time.Time
	isSleeping   bool
	isLearning   bool
	learnCancel  context.CancelFunc
	state        LearningState
	statePath    string
}

func NewSleepManager(cfg config.Config, client *ollama.Client) *SleepManager {
	return &SleepManager{
		cfg:          cfg,
		client:       client,
		sessionStore: sessions.NewStore(cfg.SessionsPath),
		lastActivity: time.Now(),
		statePath:    filepath.Join(cfg.SessionsPath, "learning_state.json"),
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
	sm.mu.Lock()
	enabled := sm.cfg.SleepModeEnabled
	sm.mu.Unlock()

	if !enabled {
		log.Println("[sleep] Sleep Mode is disabled in config")
		return
	}

	sm.LoadState()

	log.Println("[sleep] Sleep manager background service starting...")

	inactivityThreshold := 30 * time.Minute
	sm.mu.RLock()
	threshStr := sm.cfg.SleepModeInactivityThreshold
	sm.mu.RUnlock()
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
						sm.mu.Unlock()
						log.Printf("[sleep] System has been idle for %v. Activating sleep mode learning...", now.Sub(lastAct))
						go sm.runLearningCycle(ctx)
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

func (d *sleepStreamHandler) OnThinking(delta string)           {}
func (d *sleepStreamHandler) OnContent(delta string)            {}
func (d *sleepStreamHandler) OnToolCall(call ollama.ToolCall)   {}
func (d *sleepStreamHandler) OnToolStart(name string, args any) {}
func (d *sleepStreamHandler) OnToolResult(name string, result string) {}
func (d *sleepStreamHandler) OnMediaPreProcessing(content string)    {}

func (sm *SleepManager) runLearningCycle(parentCtx context.Context) {
	sm.mu.Lock()
	if sm.isLearning {
		sm.mu.Unlock()
		return
	}
	sm.isLearning = true
	ctx, cancel := context.WithCancel(parentCtx)
	sm.learnCancel = cancel
	sm.mu.Unlock()

	defer func() {
		sm.mu.Lock()
		sm.isLearning = false
		if sm.learnCancel != nil {
			sm.learnCancel = nil
		}
		sm.mu.Unlock()
	}()

	log.Println("[sleep] Continuous learning cycle started.")

	sessList, err := sm.sessionStore.List()
	if err != nil {
		log.Printf("[sleep] Error listing sessions: %v", err)
		return
	}

	var sessionToAnalyze string
	sm.mu.RLock()
	analyzed := make(map[string]bool)
	for _, id := range sm.state.AnalyzedSessions {
		analyzed[id] = true
	}
	sm.mu.RUnlock()

	for _, s := range sessList {
		if !analyzed[s.ID] {
			sessionToAnalyze = s.ID
			break
		}
	}

	if sessionToAnalyze == "" {
		log.Println("[sleep] No new sessions to analyze.")
		return
	}

	sess, err := sm.sessionStore.Get(sessionToAnalyze)
	if err != nil {
		log.Printf("[sleep] Error loading session %s: %v", sessionToAnalyze, err)
		return
	}

	if len(sess.Messages) == 0 {
		sm.mu.Lock()
		sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, sessionToAnalyze)
		sm.mu.Unlock()
		_ = sm.SaveState()
		return
	}

	log.Printf("[sleep] Analyzing session %s (%s)...", sessionToAnalyze, sess.Title)

	var historyText strings.Builder
	for idx, raw := range sess.Messages {
		var m struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &m); err == nil {
			fmt.Fprintf(&historyText, "[Msg %d] %s: %s\n", idx+1, m.Role, m.Content)
		}
	}

	learningModel := sm.cfg.OllamaModelLearning
	if learningModel == "" {
		learningModel = sm.cfg.OllamaDefaultModel
	}
	if learningModel == "" {
		log.Println("[sleep] No default or learning model configured. Aborting cycle.")
		return
	}

	analysisPrompt := fmt.Sprintf(`You are the OllamaBot self-refining learning analyzer.
Analyze the following conversation history for:
- Potential errors, user frustration, repetitive mistakes, or areas where the assistant struggled.
- User preferences, coding style preferences, spoken language preferences, tastes, background info, or user name.

---
CONVERSATION HISTORY:
%s
---

Your goal:
1. Determine if the assistant made any mistakes, failed to solve a task, or caused user frustration. If so, create/modify the corresponding skills.
2. Identify user preferences or tastes. If found, update the User Profile at 'agent/USER_PROFILE.md' by reading it first with 'read_file' and updating it with 'Write' or 'Edit'.
3. Call the appropriate tools to make these improvements. You have access to:
   - 'skill_list', 'skill_get', 'skill_create', 'skill_edit', 'skill_delete' for skill management.
   - 'read_file', 'Write', 'Edit' to update 'agent/SOUL.md' or 'agent/USER_PROFILE.md'.

If no changes are needed to skills or the user profile, respond explaining why, and do not call any tools.
Provide a clear final summary of what you did.`, historyText.String())

	registry := tools.NewRegistry(sm.cfg.WebSearchEnabled, sm.cfg.Workspace, nil, sm.client, sm.cfg.OllamaModelEmbed, tools.SearchConfig{
		Providers:    sm.cfg.SearchProviders,
		BraveAPIKey:  sm.cfg.BraveSearchAPIKey,
		TavilyAPIKey: sm.cfg.TavilyAPIKey,
	})
	registry.SetSkillsPath(sm.cfg.SkillsPath)

	reflectorAgent := agent.NewAgent(sm.cfg, sm.client, registry)

	systemPrompt := `You are the OllamaBot Self-Improvement Reflector.
You operate in the background during sleep mode.
You have access to tools to modify skills, identity, and the user profile.
Be precise and constructive. Focus on creating high-quality, actionable, clear skill guidelines.
When editing or creating skills, use standard SKILL.md format:
- Frontmatter containing name, description, homepage.
- ## Description header.
- ## Instructions header containing list items starting with - [ ], -, or numbered steps.

Keep the user profile ('agent/USER_PROFILE.md') structured:
- Name
- Preferred Languages
- Coding Styles & Preferences
- Tastes & Interests
- General Context & Past Decisions

Log all updates you make in the audit log ('skills/audit_log.md'). You can write or edit this file using 'Write' or 'Edit' tools. Each log entry must include:
- Date/time
- Chat Session ID analyzed
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

	if len(actions) > 0 {
		sm.appendToAuditLog(sessionToAnalyze, actions, summary)
	}

	sm.mu.Lock()
	sm.state.AnalyzedSessions = append(sm.state.AnalyzedSessions, sessionToAnalyze)
	sm.mu.Unlock()
	_ = sm.SaveState()

	log.Printf("[sleep] Analysis of session %s completed successfully.", sessionToAnalyze)
}

func (sm *SleepManager) appendToAuditLog(sessionID string, actions []string, summary string) {
	auditPath := filepath.Join(sm.cfg.SkillsPath, "audit_log.md")

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
