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

func (h *testOptStreamHandler) OnThinking(string)                   {}
func (h *testOptStreamHandler) OnContent(string)                    {}
func (h *testOptStreamHandler) OnToolCall(ollama.ToolCall)          {}
func (h *testOptStreamHandler) OnToolStart(string, any, string)     {}
func (h *testOptStreamHandler) OnToolResult(string, string, string) {}
func (h *testOptStreamHandler) OnMediaPreProcessing(string)         {}
func (h *testOptStreamHandler) OnDone(ollama.ChatResponse)          {}
func (h *testOptStreamHandler) OnEvent(kind string, data any)       {}

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

func TestCompactToolOutputs(t *testing.T) {
	// Case 1: Less than 3 tool results -> no compaction
	msgs1 := []ollama.Message{
		{Role: "user", Content: "initial prompt"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: strings.Repeat("A", 500)},
		{Role: "assistant", Content: "calling tool 2"},
		{Role: "tool", Content: strings.Repeat("B", 500)},
	}
	_, count1 := compactToolOutputs(msgs1)
	if count1 != 0 {
		t.Errorf("expected 0 compacted messages for <3 tool outputs, got %d", count1)
	}

	// Case 2: 4 tool results -> compact the first 2, keep the last 2 intact
	msgs2 := []ollama.Message{
		{Role: "user", Content: "initial prompt"},
		{Role: "assistant", Content: "calling tool 1"},
		{Role: "tool", Content: "PREFIX1_" + strings.Repeat("X", 500) + "_SUFFIX1"},
		{Role: "assistant", Content: "calling tool 2"},
		{Role: "tool", Content: "PREFIX2_" + strings.Repeat("Y", 500) + "_SUFFIX2"},
		{Role: "assistant", Content: "calling tool 3"},
		{Role: "tool", Content: "TOOL_3_RECENT_OUTPUT"},
		{Role: "assistant", Content: "calling tool 4"},
		{Role: "tool", Content: "TOOL_4_RECENT_OUTPUT"},
	}

	compacted, count2 := compactToolOutputs(msgs2)
	if count2 != 2 {
		t.Errorf("expected 2 compacted messages, got %d", count2)
	}

	// Verify older tool 1 was compacted
	if !strings.Contains(compacted[2].Content, "Tool output truncated for context budget") {
		t.Errorf("expected tool 1 to be truncated, got: %s", compacted[2].Content)
	}
	if !strings.Contains(compacted[2].Content, "PREFIX1_") || !strings.Contains(compacted[2].Content, "_SUFFIX1") {
		t.Errorf("expected tool 1 to keep prefix and suffix")
	}

	// Verify recent tool 3 and 4 remain untouched
	if compacted[6].Content != "TOOL_3_RECENT_OUTPUT" {
		t.Errorf("expected recent tool 3 untouched, got: %s", compacted[6].Content)
	}
	if compacted[8].Content != "TOOL_4_RECENT_OUTPUT" {
		t.Errorf("expected recent tool 4 untouched, got: %s", compacted[8].Content)
	}
}
