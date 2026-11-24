package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestPlanMonitorResumesDeferredPlan(t *testing.T) {
	sessionsPath := t.TempDir()
	workspace := t.TempDir()
	memoryPath := t.TempDir()

	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "plan-monitor-test", Title: "Test", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	plan, err := sessions.ActivatePlan(store, sess.ID, "Do monitored work", []string{"Read file"})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	plan.Status = sessions.PlanStatusDeferred
	plan.DeferredUntil = &past
	plan.DeferredReason = "scheduled follow-up"
	plan.FollowUpSummary = "Read the file."
	sess, err = store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	sess.ActivePlan = &plan
	if err := store.Save(sess); err != nil {
		t.Fatalf("save deferred plan: %v", err)
	}

	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			chatCalls++
			todoArgs, _ := json.Marshal(map[string]any{
				"merge": true,
				"todos": []map[string]any{{
					"id":      "read-file",
					"content": "Read file",
					"status":  "completed",
				}},
			})
			completeArgs, _ := json.Marshal(map[string]any{"note": "read file"})
			var resp ollama.ChatResponse
			if chatCalls == 1 {
				resp = ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role: "assistant",
						ToolCalls: []ollama.ToolCall{{
							Type: "function",
							Function: ollama.ToolFunction{
								Name:      "todo_write",
								Arguments: todoArgs,
							},
						}, {
							Type: "function",
							Function: ollama.ToolFunction{
								Name:      "complete_plan_step",
								Arguments: completeArgs,
							},
						}},
					},
				}
			} else {
				resp = ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role:    "assistant",
						Content: "The monitored plan is complete.",
					},
				}
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		Workspace:           workspace,
		SessionsPath:        sessionsPath,
		MemoryPath:          memoryPath,
		OllamaBaseURL:       server.URL,
		OllamaDefaultModel:  "test-model",
		OllamaThinkEnabled:  false,
		WebSearchEnabled:    false,
		SessionAutoName:     false,
		PlanConfirmation:    "smart",
		OllamaModelSubagent: "test-model",
	}
	client := ollama.NewClient(server.URL)
	pm := NewPlanMonitor(config.NewManager(cfg), client, memory.NewStore(memoryPath))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pm.Tick(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := store.Get(sess.ID)
		if err == nil && loaded.ActivePlan != nil && loaded.ActivePlan.Status == sessions.PlanStatusCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	loaded, _ := store.Get(sess.ID)
	t.Fatalf("expected completed monitored plan, got %#v", loaded.ActivePlan)
}

// A scheduled run logs into its own session and can approve its own plan with
// nobody present. The monitor must leave those plans alone instead of resuming
// them forever.
func TestPlanMonitorSkipsUnattendedSession(t *testing.T) {
	sessionsPath := t.TempDir()
	memoryPath := t.TempDir()

	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "scheduler-task-session", Title: "⏰ News digest", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	plan, err := sessions.ActivatePlan(store, sess.ID, "Configure the scheduled task", []string{"Ask the user for the hour", "Create the task"})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	plan.Status = sessions.PlanStatusDeferred
	plan.DeferredUntil = &past
	sess, err = store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	sess.ActivePlan = &plan
	if err := store.Save(sess); err != nil {
		t.Fatalf("save deferred plan: %v", err)
	}

	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			chatCalls++
			t.Errorf("plan monitor called the model for an unattended session")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollama.ChatResponse{Done: true})
	}))
	defer server.Close()

	cfg := config.Config{
		Workspace:          t.TempDir(),
		SessionsPath:       sessionsPath,
		MemoryPath:         memoryPath,
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
		PlanConfirmation:   "smart",
	}
	pm := NewPlanMonitor(config.NewManager(cfg), ollama.NewClient(server.URL), memory.NewStore(memoryPath))
	pm.SetUnattendedSessionChecker(func(sessionID string) bool { return sessionID == sess.ID })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pm.Tick(ctx)
	time.Sleep(200 * time.Millisecond)

	if chatCalls != 0 {
		t.Fatalf("expected no model call, got %d", chatCalls)
	}
	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Status != sessions.PlanStatusDeferred {
		t.Fatalf("expected the unattended plan to stay untouched, got %#v", loaded.ActivePlan)
	}
}

func TestPlanMonitorPausesStalledPlanAfterMaxAttempts(t *testing.T) {
	sessionsPath := t.TempDir()
	memoryPath := t.TempDir()

	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "plan-monitor-stall", Title: "Stall", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	plan, err := sessions.ActivatePlan(store, sess.ID, "Stuck work", []string{"Step 1"})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	plan.LastProgressAt = time.Now().Add(-time.Hour)
	sess, err = store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	sess.ActivePlan = &plan
	if err := store.Save(sess); err != nil {
		t.Fatalf("save active plan: %v", err)
	}

	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
			return
		}
		if r.URL.Path == "/api/chat" {
			chatCalls++
			http.Error(w, "upstream error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.Config{
		Workspace:          t.TempDir(),
		SessionsPath:       sessionsPath,
		MemoryPath:         memoryPath,
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
		PlanConfirmation:   "smart",
	}
	pm := NewPlanMonitor(config.NewManager(cfg), ollama.NewClient(server.URL), memory.NewStore(memoryPath))
	pm.staleAfter = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < maxPlanMonitorAttempts; i++ {
		pm.Tick(ctx)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			pm.mu.RLock()
			working := pm.isWorking[sess.ID]
			pm.mu.RUnlock()
			if !working && chatCalls >= i+1 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(10 * time.Millisecond)
	}

	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Status != sessions.PlanStatusDeferred || loaded.ActivePlan.DeferredUntil != nil {
		t.Fatalf("expected stalled plan to be paused indefinitely, got %#v", loaded.ActivePlan)
	}

	callsBeforeExtraTick := chatCalls
	pm.Tick(ctx)
	time.Sleep(100 * time.Millisecond)
	if chatCalls != callsBeforeExtraTick {
		t.Fatalf("expected no further model calls after plan paused, got %d -> %d", callsBeforeExtraTick, chatCalls)
	}
}

func TestPlanMonitorPausesRepeatedlyDeferredPlanWithoutProgress(t *testing.T) {
	sessionsPath := t.TempDir()
	memoryPath := t.TempDir()

	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "plan-monitor-defer-loop", Title: "DeferLoop", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	plan, err := sessions.ActivatePlan(store, sess.ID, "Wait on schedule", []string{"Step 1"})
	if err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	plan.Status = sessions.PlanStatusDeferred
	plan.DeferredUntil = &past
	sess, err = store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	sess.ActivePlan = &plan
	if err := store.Save(sess); err != nil {
		t.Fatalf("save deferred plan: %v", err)
	}

	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
			return
		}
		if r.URL.Path == "/api/chat" {
			chatCalls++
			// Alternate: odd calls invoke defer_plan_continuation; even calls return final text.
			if chatCalls%2 == 1 {
				deferArgs, _ := json.Marshal(map[string]any{
					"reason":            "waiting",
					"resume_after":      "1ms",
					"follow_up_summary": "still waiting",
					"user_message":      "I will check again shortly.",
				})
				_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role: "assistant",
						ToolCalls: []ollama.ToolCall{{
							Type: "function",
							Function: ollama.ToolFunction{
								Name:      "defer_plan_continuation",
								Arguments: deferArgs,
							},
						}},
					},
				})
			} else {
				_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
					Done:    true,
					Message: ollama.Message{Role: "assistant", Content: "Deferred."},
				})
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.Config{
		Workspace:          t.TempDir(),
		SessionsPath:       sessionsPath,
		MemoryPath:         memoryPath,
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
		PlanConfirmation:   "smart",
	}
	pm := NewPlanMonitor(config.NewManager(cfg), ollama.NewClient(server.URL), memory.NewStore(memoryPath))
	pm.staleAfter = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < maxPlanMonitorAttempts; i++ {
		prevCalls := chatCalls
		pm.Tick(ctx)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			pm.mu.RLock()
			working := pm.isWorking[sess.ID]
			pm.mu.RUnlock()
			if !working && chatCalls > prevCalls {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(15 * time.Millisecond)
	}

	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Status != sessions.PlanStatusDeferred || loaded.ActivePlan.DeferredUntil != nil {
		t.Fatalf("expected repeatedly deferred plan to be paused indefinitely, got %#v", loaded.ActivePlan)
	}

	callsBeforeExtraTick := chatCalls
	pm.Tick(ctx)
	time.Sleep(100 * time.Millisecond)
	if chatCalls != callsBeforeExtraTick {
		t.Fatalf("expected no further model calls after paused deferral loop, got %d -> %d", callsBeforeExtraTick, chatCalls)
	}
}

func TestPlanMonitorRespectsShouldThinkAndReleasesSlotDuringApproval(t *testing.T) {
	sessionsPath := t.TempDir()
	workspace := t.TempDir()
	memoryPath := t.TempDir()

	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "plan-approval-slot", Title: "Plan Slot Test", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := sessions.ActivatePlan(store, sess.ID, "Deploy safely", []string{"Run command"}); err != nil {
		t.Fatalf("activate plan: %v", err)
	}

	cfg := config.Config{
		Workspace:          workspace,
		SessionsPath:       sessionsPath,
		MemoryPath:         memoryPath,
		OllamaDefaultModel: "uncached-no-think-model",
		OllamaThinkEnabled: true,
		PlanConfirmation:   "smart",
	}
	approvalSvc := sessions.NewApprovalService(store, config.NewManager(cfg))

	approvalReached := make(chan string, 1)
	approvalSvc.RegisterNotifier(sess.ID, func(_ string, ap sessions.PendingApproval) {
		approvalReached <- ap.ID
	})

	chatTurn := 0
	sawThinkTrue := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			var req ollama.ChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if b, ok := req.Think.(bool); ok && b {
				sawThinkTrue = true
			}
			chatTurn++
			if chatTurn == 1 {
				// Request risky tool (git push) so ApprovalService blocks awaiting user decision.
				args, _ := json.Marshal(map[string]any{"command": "git", "args": []any{"push"}})
				_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role: "assistant",
						ToolCalls: []ollama.ToolCall{{
							Type:     "function",
							Function: ollama.ToolFunction{Name: "execute_command", Arguments: args},
						}},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done:    true,
				Message: ollama.Message{Role: "assistant", Content: "Stopped after denial."},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	pm := NewPlanMonitor(config.NewManager(cfg), ollama.NewClient(server.URL), memory.NewStore(memoryPath))
	pm.SetApprovalService(approvalSvc)
	pm.staleAfter = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pm.Tick(ctx)

	var approvalID string
	select {
	case approvalID = <-approvalReached:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval request: %v", ctx.Err())
	}

	// While PlanMonitor waits on the user's approval decision, the background slot must be free.
	if rel := sessions.TryAcquireBackgroundSlot(); rel == nil {
		t.Fatal("expected global background slot to be released while waiting for approval")
	} else {
		rel()
	}

	if err := approvalSvc.RespondApproval(approvalID, sessions.ApprovalDecision{Approved: false}); err != nil {
		t.Fatalf("respond approval: %v", err)
	}

	// Wait for resumePlan goroutine to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pm.mu.RLock()
		working := pm.isWorking[sess.ID]
		pm.mu.RUnlock()
		if !working {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if sawThinkTrue {
		t.Fatal("expected PlanMonitor to respect ShouldThink and send think=false for model without confirmed thinking capability")
	}
}
