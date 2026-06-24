package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
								Name:      "TodoWrite",
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

	a := NewAgent(cfg, client, registry)
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
								Name:      "TodoWrite",
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

	a := NewAgent(cfg, client, registry)
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
