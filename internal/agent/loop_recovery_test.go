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
