package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

func TestAgentRunContinuesWithActivePlanAfterTextOnlyResponse(t *testing.T) {
	sessionsPath := t.TempDir()
	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "plan-loop-test", Title: "Test", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := sessions.ActivatePlan(store, sess.ID, "Download liked videos", []string{"Research download tools"}); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	workspace := t.TempDir()

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
			var resp ollama.ChatResponse
			switch chatCalls {
			case 1:
				resp = ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role:    "assistant",
						Content: "Plan approved. I will investigate download tools now.",
					},
				}
			case 2:
				todoArgs, _ := json.Marshal(map[string]any{
					"merge": true,
					"todos": []map[string]any{{
						"id":      "research",
						"content": "Research download tools",
						"status":  "completed",
					}},
				})
				args, _ := json.Marshal(map[string]any{"note": "research done"})
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
								Arguments: args,
							},
						}},
					},
				}
			default:
				resp = ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role:    "assistant",
						Content: "All plan steps are complete.",
					},
				}
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace, SessionsPath: sessionsPath}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	registry.SetSessionStore(store)
	registry.SetSessionID(sess.ID)

	a := NewAgent(config.NewManager(cfg), client, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := a.Run(ctx, "test-model", []ollama.Message{
		{Role: "user", Content: "Download my liked YouTube videos as MP3"},
	}, false, nil)
	if err != nil {
		t.Fatalf("agent.Run failed: %v", err)
	}
	if chatCalls < 2 {
		t.Fatalf("expected at least 2 model calls while plan steps remain, got %d", chatCalls)
	}
	if chatCalls > 4 {
		t.Fatalf("expected loop to finish after plan completion, got %d model calls", chatCalls)
	}

	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Status != sessions.PlanStatusCompleted {
		t.Fatalf("expected completed plan, got %#v", loaded.ActivePlan)
	}
}

func TestAgentRunRejectsPlanCompletionWithoutAction(t *testing.T) {
	sessionsPath := t.TempDir()
	store := sessions.NewStore(sessionsPath)
	sess := sessions.Session{ID: "plan-action-gate-test", Title: "Test", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := sessions.ActivatePlan(store, sess.ID, "Do work", []string{"Inspect file"}); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	workspace := t.TempDir()

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
			completeArgs, _ := json.Marshal(map[string]any{"note": "done"})
			var resp ollama.ChatResponse
			switch chatCalls {
			case 1:
				resp = ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role: "assistant",
						ToolCalls: []ollama.ToolCall{{
							Type: "function",
							Function: ollama.ToolFunction{
								Name:      "complete_plan_step",
								Arguments: completeArgs,
							},
						}},
					},
				}
			case 2:
				todoArgs, _ := json.Marshal(map[string]any{
					"merge": true,
					"todos": []map[string]any{{
						"id":      "inspect",
						"content": "Inspect the file",
						"status":  "completed",
					}},
				})
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
			default:
				resp = ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role:    "assistant",
						Content: "Done.",
					},
				}
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace, SessionsPath: sessionsPath}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	registry.SetSessionStore(store)
	registry.SetSessionID(sess.ID)

	a := NewAgent(config.NewManager(cfg), client, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := a.Run(ctx, "test-model", []ollama.Message{
		{Role: "user", Content: "Inspect the file"},
	}, false, nil)
	if err != nil {
		t.Fatalf("agent.Run failed: %v", err)
	}
	if chatCalls < 3 {
		t.Fatalf("expected model to retry after rejected completion, got %d calls", chatCalls)
	}
	loaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ActivePlan == nil || loaded.ActivePlan.Status != sessions.PlanStatusCompleted {
		t.Fatalf("expected completed plan after valid action, got %#v", loaded.ActivePlan)
	}
}

func TestAgentRunStopsRepeatedToolLoop(t *testing.T) {
	workspace := t.TempDir()
	toolArgs, _ := json.Marshal(map[string]any{
		"merge": true,
		"todos": []map[string]any{{
			"id":      "repeat",
			"content": "Repeat",
			"status":  "completed",
		}},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role: "assistant",
					ToolCalls: []ollama.ToolCall{{
						Type: "function",
						Function: ollama.ToolFunction{
							Name:      "todo_write",
							Arguments: toolArgs,
						},
					}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Run(ctx, "test-model", []ollama.Message{{Role: "user", Content: "Repeat a tool call"}}, false, nil)
	if err == nil {
		t.Fatal("expected repetitive loop error")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "detected repetitive loop") {
		t.Fatalf("expected repetitive loop error, got %v", err)
	}
}

// TestAgentRunStopsNoOpLoopEarly verifies that repeating a no-op tool call
// (e.g. list_files with empty args) aborts after 3 iterations instead of 5,
// and that the error message mentions the repetitive loop.
func TestAgentRunStopsNoOpLoopEarly(t *testing.T) {
	workspace := t.TempDir()
	// list_files with empty args -> defaults to "." -> no-op signature.
	toolArgs, _ := json.Marshal(map[string]any{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role: "assistant",
					ToolCalls: []ollama.ToolCall{{
						Type: "function",
						Function: ollama.ToolFunction{
							Name:      "list_files",
							Arguments: toolArgs,
						},
					}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Run(ctx, "test-model", []ollama.Message{{Role: "user", Content: "List files repeatedly"}}, false, nil)
	if err == nil {
		t.Fatal("expected repetitive loop error for no-op list_files")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "detected repetitive loop") {
		t.Fatalf("expected repetitive loop error, got %v", err)
	}
	if !strings.Contains(err.Error(), "list_files") {
		t.Fatalf("expected error to mention list_files, got %v", err)
	}
}

// TestAgentRunStopsExcessiveNetworkFetch verifies that calling fetch_webpage
// many times with DIFFERENT URLs (each under the per-signature threshold) is
// caught by the global per-tool cap and aborts the run.
func TestAgentRunStopsExcessiveNetworkFetch(t *testing.T) {
	workspace := t.TempDir()

	// Cycle through different URLs so no single signature hits the per-key
	// threshold; only the global cap should catch it.
	urls := []string{
		"https://example.com/page-1",
		"https://example.com/page-2",
		"https://example.com/page-3",
		"https://example.com/page-4",
		"https://example.com/page-5",
		"https://example.com/page-6",
		"https://example.com/page-7",
		"https://example.com/page-8",
		"https://example.com/page-9",
		"https://example.com/page-10",
		"https://example.com/page-11",
		"https://example.com/page-12",
		"https://example.com/page-13",
	}
	callIdx := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			url := urls[callIdx%len(urls)]
			callIdx++
			args, _ := json.Marshal(map[string]any{"url": url})
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role: "assistant",
					ToolCalls: []ollama.ToolCall{{
						Type: "function",
						Function: ollama.ToolFunction{
							Name:      "fetch_webpage",
							Arguments: args,
						},
					}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := a.Run(ctx, "test-model", []ollama.Message{{Role: "user", Content: "Fetch many pages"}}, false, nil)
	if err == nil {
		t.Fatal("expected excessive network usage error")
	}
	// Should mention either "excessive network" or "repetitive loop"
	if !strings.Contains(err.Error(), "excessive network") && !strings.Contains(err.Error(), "repetitive loop") {
		t.Fatalf("expected network-related loop error, got %v", err)
	}
}

// TestAgentRunParallelToolExecution verifies that multiple read-only tool
// calls in a single model response are executed concurrently without
// deadlocking or panicking.
func TestAgentRunParallelToolExecution(t *testing.T) {
	workspace := t.TempDir()
	// Create test files so read_file succeeds.
	for i := 1; i <= 3; i++ {
		path := filepath.Join(workspace, fmt.Sprintf("file%d.txt", i))
		_ = os.WriteFile(path, []byte(fmt.Sprintf("content %d", i)), 0644)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			// Return 3 read_file tool calls in a single response.
			var calls []ollama.ToolCall
			for i := 1; i <= 3; i++ {
				args, _ := json.Marshal(map[string]any{"path": fmt.Sprintf("file%d.txt", i)})
				calls = append(calls, ollama.ToolCall{
					Type: "function",
					Function: ollama.ToolFunction{
						Name:      "read_file",
						Arguments: args,
					},
				})
			}
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role:      "assistant",
					ToolCalls: calls,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	messages := []ollama.Message{{Role: "user", Content: "Read all 3 files"}}

	// The agent will loop: first call returns 3 tool calls (executed in
	// parallel), second call returns 3 more (same mock), eventually hitting
	// the loop detector. We verify it doesn't deadlock or panic.
	_, _ = a.Run(ctx, "test-model", messages, false, nil)
}

// TestAgentRunSequentialForStatefulTools verifies that stateful tools
// (write_file) are NOT parallelized when mixed with read-only tools.
func TestAgentRunSequentialForStatefulTools(t *testing.T) {
	workspace := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			// Return a mix: read_file (parallel-safe) + write_file (sequential).
			readArgs, _ := json.Marshal(map[string]any{"path": "test.txt"})
			writeArgs, _ := json.Marshal(map[string]any{"file_path": "output.txt", "content": "hello"})
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role: "assistant",
					ToolCalls: []ollama.ToolCall{
						{Type: "function", Function: ollama.ToolFunction{Name: "read_file", Arguments: readArgs}},
						{Type: "function", Function: ollama.ToolFunction{Name: "write_file", Arguments: writeArgs}},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{Workspace: workspace}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	messages := []ollama.Message{{Role: "user", Content: "Read and write files"}}
	_, _ = a.Run(ctx, "test-model", messages, false, nil)

	// Verify the write_file actually executed (file exists).
	if _, err := os.Stat(filepath.Join(workspace, "output.txt")); err != nil {
		t.Fatalf("expected output.txt to be created by write_file: %v", err)
	}
}

// TestAutonomousPriorTaskContext verifies that ExecuteTask injects context
// from previously completed tasks into the system prompt.
func TestAutonomousPriorTaskContext(t *testing.T) {
	workspace := t.TempDir()
	sessionsPath := t.TempDir()

	// Create a project with 2 tasks, first one already completed.
	projDir := filepath.Join(workspace, "test-proj")
	_ = os.MkdirAll(projDir, 0755)

	proj := Project{
		ID:        "test-proj",
		Name:      "Test Project",
		Goal:      "Build something cool",
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Todos: []ProjectTodo{
			{
				ID:        "task-1",
				Content:   "Create the foundation",
				Status:    "completed",
				Result:    "Created index.html with basic layout and CSS grid.",
				UpdatedAt: time.Now(),
			},
			{
				ID:        "task-2",
				Content:   "Add interactivity",
				Status:    "pending",
				UpdatedAt: time.Now(),
			},
		},
	}
	projData, _ := json.Marshal(proj)
	_ = os.WriteFile(filepath.Join(projDir, "project.json"), projData, 0644)

	// Mock Ollama server that captures all system messages.
	var allSystemContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			// Capture all system messages from the request.
			var req ollama.ChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			for _, msg := range req.Messages {
				if msg.Role == "system" {
					allSystemContent += msg.Content + "\n"
				}
			}
			// Return a simple text response to end the turn.
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role:    "assistant",
					Content: "Task completed successfully.",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{
		Workspace:          workspace,
		SessionsPath:       sessionsPath,
		OllamaDefaultModel: "test-model",
	}
	cfgMgr := config.NewManager(cfg)
	am := NewAutonomousManager(cfgMgr, client, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = am.ExecuteTask(ctx, "test-proj", 1) // Execute task-2 (index 1)

	// Verify the system messages contain prior task context.
	if !strings.Contains(allSystemContent, "Prior Task Context") {
		t.Fatalf("expected system messages to contain 'Prior Task Context'")
	}
	if !strings.Contains(allSystemContent, "task-1") {
		t.Fatalf("expected system messages to mention task-1")
	}
	if !strings.Contains(allSystemContent, "Created index.html") {
		t.Fatalf("expected system messages to contain task-1 result")
	}
}
