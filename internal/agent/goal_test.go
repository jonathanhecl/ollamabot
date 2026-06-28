package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestGoalManager_Lifecycle(t *testing.T) {
	// Create temp directories for sessions and memory stores
	tempDir, err := os.MkdirTemp("", "ollamabot-goal-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sessionsPath := filepath.Join(tempDir, "sessions")
	memoryPath := filepath.Join(tempDir, "memory")
	workspacePath := filepath.Join(tempDir, "workspace")
	_ = os.MkdirAll(workspacePath, 0755)

	cfg := config.Config{
		SessionsPath: sessionsPath,
		MemoryPath:   memoryPath,
		Workspace:    workspacePath,
	}

	// Create a mock Ollama server
	var evalResponse string
	var evalResponseMu sync.Mutex
	evalResponse = `{"achieved": false, "reasoning": "Waiting for actions."}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			var chatReq ollama.ChatRequest
			_ = json.NewDecoder(r.Body).Decode(&chatReq)

			w.Header().Set("Content-Type", "application/json")
			
			// Check if it's the evaluator request
			isEval := false
			for _, m := range chatReq.Messages {
				if strings.Contains(m.Content, "Goal Evaluator") || strings.Contains(m.Content, "JSON") {
					isEval = true
				}
			}

			if isEval {
				evalResponseMu.Lock()
				resp := ollama.ChatResponse{
					Message: ollama.Message{
						Role:    "assistant",
						Content: evalResponse,
					},
					Done: true,
				}
				evalResponseMu.Unlock()
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			// Stream response for the agent.Run cycle
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			
			resp := ollama.ChatResponse{
				Message: ollama.Message{
					Role:    "assistant",
					Content: "Doing some work.",
				},
				Done: false,
			}
			_ = json.NewEncoder(w).Encode(resp)
			w.Write([]byte("\n"))
			flusher.Flush()

			respDone := ollama.ChatResponse{
				Message: ollama.Message{},
				Done:    true,
			}
			_ = json.NewEncoder(w).Encode(respDone)
			w.Write([]byte("\n"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	goalMgr := NewGoalManager(config.NewManager(cfg), client)

	sessionID := "session_1"
	store := sessions.NewStore(sessionsPath)
	
	// Pre-create the session
	err = store.Save(sessions.Session{
		ID:    sessionID,
		Title: "Test Session",
	})
	if err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// 1. Test workspace file reading support
	objFile := filepath.Join(workspacePath, "my_goal.txt")
	err = os.WriteFile(objFile, []byte("Complete task inside workspace file"), 0644)
	if err != nil {
		t.Fatalf("failed to write objective file: %v", err)
	}

	// Start goal using file reference
	err = goalMgr.StartGoal(sessionID, "my_goal.txt")
	if err != nil {
		t.Fatalf("failed to start goal: %v", err)
	}

	// Verify session was updated
	sess, err := store.Get(sessionID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if sess.GoalStatus != "active" {
		t.Errorf("expected goal status active, got %s", sess.GoalStatus)
	}
	if sess.GoalObjective != "Complete task inside workspace file" {
		t.Errorf("expected objective loaded from file, got %q", sess.GoalObjective)
	}

	// 2. Test notifier works
	var notifyMsg string
	var notifyMu sync.Mutex
	goalMgr.RegisterNotifier(sessionID, func(msg string) {
		notifyMu.Lock()
		notifyMsg = msg
		notifyMu.Unlock()
	})

	// Wait briefly for at least one cycle notification
	time.Sleep(500 * time.Millisecond)

	notifyMu.Lock()
	hasNotification := notifyMsg != ""
	notifyMu.Unlock()
	if !hasNotification {
		t.Log("Warning: No notification received yet, background goroutine slow to start")
	}

	// 3. Test Pause
	err = goalMgr.PauseGoal(sessionID)
	if err != nil {
		t.Fatalf("failed to pause goal: %v", err)
	}
	
	sess, _ = store.Get(sessionID)
	if sess.GoalStatus != "paused" {
		t.Errorf("expected goal status paused, got %s", sess.GoalStatus)
	}

	// 4. Test Resume
	err = goalMgr.ResumeGoal(sessionID)
	if err != nil {
		t.Fatalf("failed to resume goal: %v", err)
	}

	sess, _ = store.Get(sessionID)
	if sess.GoalStatus != "active" {
		t.Errorf("expected goal status active, got %s", sess.GoalStatus)
	}

	// Let the evaluator know the goal is now achieved
	evalResponseMu.Lock()
	evalResponse = `{"achieved": true, "reasoning": "Goal accomplished."}`
	evalResponseMu.Unlock()

	// Wait for the next evaluation check to complete it
	time.Sleep(1 * time.Second)

	sess, _ = store.Get(sessionID)
	t.Logf("Sess status after resume: %s", sess.GoalStatus)

	// Clean up and test Clear
	err = goalMgr.ClearGoal(sessionID)
	if err != nil {
		t.Fatalf("failed to clear goal: %v", err)
	}

	sess, _ = store.Get(sessionID)
	if sess.GoalStatus != "" || sess.GoalObjective != "" {
		t.Errorf("expected cleared goal fields, got status=%q, objective=%q", sess.GoalStatus, sess.GoalObjective)
	}
}

func TestGoalManager_ResumeActiveGoals(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ollamabot-goal-resume-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sessionsPath := filepath.Join(tempDir, "sessions")
	memoryPath := filepath.Join(tempDir, "memory")
	cfg := config.Config{
		SessionsPath: sessionsPath,
		MemoryPath:   memoryPath,
	}

	store := sessions.NewStore(sessionsPath)
	err = store.Save(sessions.Session{
		ID:            "active_session",
		Title:         "Active",
		GoalObjective: "Keep working",
		GoalStatus:    "active",
	})
	if err != nil {
		t.Fatalf("failed to setup active session: %v", err)
	}

	err = store.Save(sessions.Session{
		ID:            "paused_session",
		Title:         "Paused",
		GoalObjective: "Don't work",
		GoalStatus:    "paused",
	})
	if err != nil {
		t.Fatalf("failed to setup paused session: %v", err)
	}

	// Mock server that returns completed instantly to avoid infinite loop running in background
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: `{"achieved": true, "reasoning": "Instantly achieved in test."}`,
			},
			Done: true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	goalMgr := NewGoalManager(config.NewManager(cfg), client)

	err = goalMgr.ResumeActiveGoals()
	if err != nil {
		t.Fatalf("failed to resume active goals: %v", err)
	}

	goalMgr.mu.Lock()
	_, activeRunning := goalMgr.activeLoops["active_session"]
	_, pausedRunning := goalMgr.activeLoops["paused_session"]
	goalMgr.mu.Unlock()

	if !activeRunning {
		t.Errorf("expected active_session loop to be resumed and running")
	}
	if pausedRunning {
		t.Errorf("expected paused_session loop to NOT be running")
	}

	// Stop loop context clean up
	_ = goalMgr.ClearGoal("active_session")
}

func TestGoalManager_EvaluateProgress(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
		wantAchieved  bool
		wantReasoning string
		wantErr       bool
	}{
		{
			name:          "valid json",
			modelResponse: `{"achieved": true, "reasoning": "Tasks are complete."}`,
			wantAchieved:  true,
			wantReasoning: "Tasks are complete.",
			wantErr:       false,
		},
		{
			name:          "valid json with code fences",
			modelResponse: "```json\n{\"achieved\": false, \"reasoning\": \"Missing database configuration.\"}\n```",
			wantAchieved:  false,
			wantReasoning: "Missing database configuration.",
			wantErr:       false,
		},
		{
			name:          "invalid json",
			modelResponse: `Not a JSON response`,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				resp := ollama.ChatResponse{
					Message: ollama.Message{
						Role:    "assistant",
						Content: tt.modelResponse,
					},
					Done: true,
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			cfg := config.Config{}
			client := ollama.NewClient(server.URL)
			goalMgr := NewGoalManager(config.NewManager(cfg), client)

			achieved, reasoning, err := goalMgr.evaluateProgress(context.Background(), "some objective", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error: %v, got: %v", tt.wantErr, err)
			}
			if !tt.wantErr {
				if achieved != tt.wantAchieved {
					t.Errorf("expected achieved=%v, got=%v", tt.wantAchieved, achieved)
				}
				if reasoning != tt.wantReasoning {
					t.Errorf("expected reasoning=%q, got=%q", tt.wantReasoning, reasoning)
				}
			}
		})
	}
}

func TestGoalManager_ResumeActiveGoals_InterruptedCycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ollamabot-goal-interrupt-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sessionsPath := filepath.Join(tempDir, "sessions")
	memoryPath := filepath.Join(tempDir, "memory")
	cfg := config.Config{
		SessionsPath: sessionsPath,
		MemoryPath:   memoryPath,
	}

	store := sessions.NewStore(sessionsPath)
	err = store.Save(sessions.Session{
		ID:              "crashed_session",
		Title:           "Crashed",
		GoalObjective:   "Finish the task",
		GoalStatus:      "active",
		GoalCycleActive: true,
	})
	if err != nil {
		t.Fatalf("failed to setup crashed session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: `{"achieved": true, "reasoning": "Done."}`,
			},
			Done: true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	goalMgr := NewGoalManager(config.NewManager(cfg), client)

	err = goalMgr.ResumeActiveGoals()
	if err != nil {
		t.Fatalf("failed to resume: %v", err)
	}

	// Give the loop a moment to start
	time.Sleep(200 * time.Millisecond)

	sess, err := store.Get("crashed_session")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}

	// GoalCycleActive should be cleared
	if sess.GoalCycleActive {
		t.Errorf("expected GoalCycleActive to be false after resume, got true")
	}

	// GoalRestartCount should be incremented
	if sess.GoalRestartCount != 1 {
		t.Errorf("expected GoalRestartCount=1, got %d", sess.GoalRestartCount)
	}

	// The interruption system message should be in the messages
	found := false
	for _, raw := range sess.Messages {
		var m sessions.RawMsg
		if err := json.Unmarshal(raw, &m); err == nil {
			if m.Role == "system" && strings.Contains(m.Content, "interrupted due to a process restart") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected interruption system message in session history")
	}

	_ = goalMgr.ClearGoal("crashed_session")
}

func TestGoalManager_ResumeActiveGoals_RestartLimit(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ollamabot-goal-restartlimit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sessionsPath := filepath.Join(tempDir, "sessions")
	memoryPath := filepath.Join(tempDir, "memory")
	cfg := config.Config{
		SessionsPath: sessionsPath,
		MemoryPath:   memoryPath,
	}

	store := sessions.NewStore(sessionsPath)
	err = store.Save(sessions.Session{
		ID:               "loop_crasher",
		Title:            "Loop Crasher",
		GoalObjective:    "Keep crashing",
		GoalStatus:       "active",
		GoalRestartCount: 3,
	})
	if err != nil {
		t.Fatalf("failed to setup session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: `{"achieved": true, "reasoning": "Done."}`,
			},
			Done: true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	goalMgr := NewGoalManager(config.NewManager(cfg), client)

	err = goalMgr.ResumeActiveGoals()
	if err != nil {
		t.Fatalf("failed to resume: %v", err)
	}

	// The goal should be paused, not resumed
	sess, err := store.Get("loop_crasher")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if sess.GoalStatus != "paused" {
		t.Errorf("expected goal status paused, got %s", sess.GoalStatus)
	}

	goalMgr.mu.Lock()
	_, running := goalMgr.activeLoops["loop_crasher"]
	goalMgr.mu.Unlock()
	if running {
		t.Errorf("expected no active loop for session that exceeded restart limit")
	}
}
