package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type testOptStreamHandler struct {
	optStarted       bool
	optEnded         bool
	optOptimized     bool
	tokensBefore     int
	tokensAfter      int
	optimizedHistory []ollama.Message
}

func (h *testOptStreamHandler) OnThinking(string)                 {}
func (h *testOptStreamHandler) OnContent(string)                  {}
func (h *testOptStreamHandler) OnToolCall(ollama.ToolCall)        {}
func (h *testOptStreamHandler) OnToolStart(string, any)           {}
func (h *testOptStreamHandler) OnToolResult(string, string)       {}
func (h *testOptStreamHandler) OnMediaPreProcessing(string)       {}
func (h *testOptStreamHandler) OnDone(ollama.ChatResponse)         {}

func (h *testOptStreamHandler) OnContextOptimizationStart(tokensBefore int, percentBefore float64) {
	h.optStarted = true
	h.tokensBefore = tokensBefore
}

func (h *testOptStreamHandler) OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64) {
	h.optEnded = true
	h.tokensAfter = tokensAfter
}

func (h *testOptStreamHandler) OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int) {
	h.optOptimized = true
	h.optimizedHistory = optimizedMessages
}

func TestContextOptimizationFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/show":
			modelInfo := map[string]any{
				"general.context_length": float64(1000),
			}
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				ModelInfo:    modelInfo,
				Capabilities: []string{"completion"},
			})
		case "/api/chat":
			// Check if this is a summarization chat call or the main stream chat call
			var req ollama.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				// Summarization request is non-streaming and contains system summary instructions
				isSummary := false
				for _, msg := range req.Messages {
					if msg.Role == "system" && strings.Contains(msg.Content, "Please summarize") {
						isSummary = true
						break
					}
				}
				if isSummary {
					_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
						Done: true,
						Message: ollama.Message{
							Role:    "assistant",
							Content: "Mocked synthesis summary of previous work.",
						},
					})
					return
				}
			}
			// Main stream loop response
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Done: true,
				Message: ollama.Message{
					Role:    "assistant",
					Content: "Main loop finished successfully",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	cfg := config.Config{
		Workspace: t.TempDir(),
	}

	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := NewAgent(config.NewManager(cfg), client, registry)

	// Create messages that are long enough to exceed the 90% threshold (900 tokens, which is ~3600 characters)
	largeContent := strings.Repeat("abcd ", 800) // 4000 characters ~ 1000 tokens

	msgs := []ollama.Message{
		{Role: "user", Content: "Previous work: " + largeContent},
		{Role: "assistant", Content: "Thinking and acting: " + largeContent, Thinking: "Some thinking: " + largeContent},
		{Role: "user", Content: "This is the last user prompt"},
	}

	handler := &testOptStreamHandler{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := a.Run(ctx, "test-model", msgs, false, handler)
	if err != nil {
		t.Fatalf("agent.Run failed: %v", err)
	}

	if !handler.optStarted {
		t.Errorf("expected context optimization to start")
	}
	if !handler.optEnded {
		t.Errorf("expected context optimization to end")
	}
	if !handler.optOptimized {
		t.Errorf("expected OnContextOptimized to be called")
	}

	// The optimized history should start with the system summary message
	if len(handler.optimizedHistory) == 0 {
		t.Fatalf("expected optimized history, got empty")
	}
	firstMsg := handler.optimizedHistory[0]
	if firstMsg.Role != "system" || !strings.Contains(firstMsg.Content, "Mocked synthesis summary") {
		t.Errorf("expected first message to be system summary, got role=%s, content=%s", firstMsg.Role, firstMsg.Content)
	}

	// The second message in optimized history should be the last user message we kept
	if len(handler.optimizedHistory) < 2 {
		t.Fatalf("expected at least 2 messages in optimized history, got %d", len(handler.optimizedHistory))
	}
	secondMsg := handler.optimizedHistory[1]
	if secondMsg.Role != "user" || secondMsg.Content != "This is the last user prompt" {
		t.Errorf("expected second message to be last user prompt, got role=%s, content=%s", secondMsg.Role, secondMsg.Content)
	}
}
