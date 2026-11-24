package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

func TestLoopRecoveryWarningDetection(t *testing.T) {
	tests := []struct {
		name                 string
		result               string
		isDeniedOrTimeout    bool
		isMCPValidationError bool
	}{
		{
			name:                 "User denial",
			result:               "Error: tool approval failed: Denied by user.",
			isDeniedOrTimeout:    true,
			isMCPValidationError: false,
		},
		{
			name:                 "Lowercase tool denial",
			result:               "Error: Execution denied by user.",
			isDeniedOrTimeout:    true,
			isMCPValidationError: false,
		},
		{
			name:                 "Approval timeout",
			result:               "Error: tool approval failed: approval timeout",
			isDeniedOrTimeout:    true,
			isMCPValidationError: false,
		},
		{
			name:                 "MCP schema 32602 error",
			result:               "Error: tool returned error: MCP error -32602: Input validation error",
			isDeniedOrTimeout:    false,
			isMCPValidationError: true,
		},
		{
			name:                 "Normal error",
			result:               "Error: file not found",
			isDeniedOrTimeout:    false,
			isMCPValidationError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lower := strings.ToLower(tc.result)
			denied := strings.Contains(lower, "denied by user") || strings.Contains(lower, "approval timeout") || strings.Contains(lower, "approval failed") || strings.Contains(lower, "deadline exceeded")
			mcpErr := strings.Contains(tc.result, "MCP error -32602") || strings.Contains(tc.result, "Input validation error")

			if denied != tc.isDeniedOrTimeout {
				t.Errorf("isDeniedOrTimeout got %v, expected %v", denied, tc.isDeniedOrTimeout)
			}
			if mcpErr != tc.isMCPValidationError {
				t.Errorf("isMCPValidationError got %v, expected %v", mcpErr, tc.isMCPValidationError)
			}
		})
	}
}

type denyAllApprovals struct{}

func (denyAllApprovals) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	return false, nil
}

func TestAgentLoopAbortsRepeatedDeniedToolWithDifferentArgs(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			_, _ = io.Copy(io.Discard, r.Body)
			calls++
			args, _ := json.Marshal(map[string]any{
				"file_path": fmt.Sprintf("../outside_%d.txt", calls),
				"contents":  "blocked",
			})
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role: "assistant",
					ToolCalls: []ollama.ToolCall{{
						Type: "function",
						Function: ollama.ToolFunction{
							Name:      "write_file",
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

	cfg := config.Config{
		Workspace:          t.TempDir(),
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
		PlanConfirmation:   "smart",
	}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, nil, "", tools.SearchConfig{})
	registry.SetApprovalHandler(denyAllApprovals{})
	registry.SetUnattended(true)

	a := NewAgent(config.NewManager(cfg), ollama.NewClient(server.URL), registry)
	_, err := a.Run(context.Background(), "test-model", []ollama.Message{{Role: "user", Content: "Write a file"}}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "was denied/timed out") {
		t.Fatalf("expected loop abort on repeated denied tool, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected abort after 2 calls, got %d", calls)
	}
}

func TestAgentUnattendedDoesNotInjectPlanOrClarificationPrompts(t *testing.T) {
	var captured ollama.ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &captured)
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done:    true,
				Message: ollama.Message{Role: "assistant", Content: "All done."},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		Workspace:          t.TempDir(),
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
		PlanConfirmation:   "always",
	}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, nil, "", tools.SearchConfig{})
	registry.SetChannel("scheduler")
	registry.SetUnattended(true)

	a := NewAgent(config.NewManager(cfg), ollama.NewClient(server.URL), registry)
	if _, err := a.Run(context.Background(), "test-model", []ollama.Message{{Role: "user", Content: "Summarize today's updates across multiple sources"}}, false, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sys strings.Builder
	for _, msg := range captured.Messages {
		if msg.Role == "system" {
			sys.WriteString(msg.Content)
			sys.WriteString("\n")
		}
	}
	for _, banned := range []string{
		"MUST first call the 'present_plan' tool",
		"MUST use the 'ask_clarification' tool",
	} {
		if strings.Contains(sys.String(), banned) {
			t.Fatalf("unattended prompt contains contradictory instruction %q", banned)
		}
	}
}

func TestAgentUnattendedAbortsExcessiveNetworkCalls(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			_, _ = io.Copy(io.Discard, r.Body)
			calls++
			args, _ := json.Marshal(map[string]any{
				"url": fmt.Sprintf("http://127.0.0.1:1/page_%d", calls),
			})
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

	cfg := config.Config{
		Workspace:          t.TempDir(),
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "test-model",
		WebSearchEnabled:   true,
	}
	registry := tools.NewRegistry(true, cfg.Workspace, nil, nil, "", tools.SearchConfig{})
	registry.SetChannel("scheduler")
	registry.SetUnattended(true)

	a := NewAgent(config.NewManager(cfg), ollama.NewClient(server.URL), registry)
	_, err := a.Run(context.Background(), "test-model", []ollama.Message{{Role: "user", Content: "Fetch pages"}}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "excessive network calls") {
		t.Fatalf("expected abort on excessive network calls, got %v", err)
	}
	if calls != 10 {
		t.Fatalf("expected abort at 10 unattended network calls, got %d", calls)
	}
}

func TestContextOptimizationEmptySummaryDoesNotLoop(t *testing.T) {
	summaryCalls := 0
	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(1000)},
			})
		case "/api/chat":
			var req ollama.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			// Summarization request is non-streaming with "Summarize this conversation".
			isSummary := false
			for _, m := range req.Messages {
				if strings.Contains(m.Content, "Summarize this conversation") {
					isSummary = true
					break
				}
			}
			if isSummary {
				summaryCalls++
				_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
					Done:    true,
					Message: ollama.Message{Role: "assistant", Content: "   "},
				})
				return
			}
			chatCalls++
			if chatCalls < 3 {
				args, _ := json.Marshal(map[string]any{"path": "."})
				_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
					Done: true,
					Message: ollama.Message{
						Role: "assistant",
						ToolCalls: []ollama.ToolCall{{
							Type:     "function",
							Function: ollama.ToolFunction{Name: "list_files", Arguments: args},
						}},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done:    true,
				Message: ollama.Message{Role: "assistant", Content: "Done."},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{Workspace: t.TempDir()}
	client := ollama.NewClient(server.URL)
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	largeContent := strings.Repeat("abcd ", 800)
	msgs := []ollama.Message{
		{Role: "user", Content: "Previous work: " + largeContent},
		{Role: "assistant", Content: "Ack: " + largeContent},
		{Role: "user", Content: "Continue"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Run(ctx, "test-model", msgs, false, nil)
	if err != nil {
		t.Fatalf("a.Run failed: %v", err)
	}
	if summaryCalls != 1 {
		t.Fatalf("expected exactly 1 summarization attempt after empty summary, got %d", summaryCalls)
	}
}

func TestMalformedXMLFallbackStopsAfterMaxRetries(t *testing.T) {
	chatCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo: map[string]any{"general.context_length": float64(8192)},
			})
		case "/api/chat":
			_, _ = io.Copy(io.Discard, r.Body)
			chatCalls++
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role:    "assistant",
					Content: `<invoke name="list_files">broken unclosed xml`,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{Workspace: t.TempDir()}
	client := ollama.NewClient(server.URL)
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	history, err := a.Run(context.Background(), "test-model", []ollama.Message{{Role: "user", Content: "List files"}}, false, nil)
	if err != nil {
		t.Fatalf("a.Run failed: %v", err)
	}
	// Initial attempt + 3 retries = 4 calls total, NOT 50.
	if chatCalls != 4 {
		t.Fatalf("expected 4 chat calls (1 + 3 malformed XML retries), got %d", chatCalls)
	}
	assistantCount := 0
	for _, msg := range history {
		if msg.Role == "assistant" {
			assistantCount++
		}
	}
	if assistantCount < 2 {
		t.Fatalf("expected malformed assistant outputs to be preserved in history, got %d assistant messages", assistantCount)
	}
}
