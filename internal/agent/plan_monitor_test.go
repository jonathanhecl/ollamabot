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
