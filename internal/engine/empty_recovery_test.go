package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

// TestProcessTurnRecoversFromThinkingOnlyLoop reproduces a turn where the
// model answers with thinking tokens only (no visible text, no tool calls)
// over and over. The turn must still end with a visible answer for the user
// instead of dying with an "empty response" error.
func TestProcessTurnRecoversFromThinkingOnlyLoop(t *testing.T) {
	runTurn := func(t *testing.T, recoveryText string) (string, error, int32) {
		t.Helper()
		var chatCalls int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/show":
				_ = json.NewEncoder(w).Encode(ollama.ShowResponse{Capabilities: []string{"completion"}})
			case "/api/ps":
				_ = json.NewEncoder(w).Encode(ollama.PsResponse{})
			case "/api/chat":
				var body struct {
					Stream bool `json:"stream"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				call := atomic.AddInt32(&chatCalls, 1)
				if !body.Stream {
					// Final recovery call: plain non-streaming answer.
					_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
						Model:        "main-model",
						Done:         true,
						DoneReason:   "stop",
						Message:      ollama.Message{Role: "assistant", Content: recoveryText},
					})
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				streamChunk(w, ollama.ChatResponse{
					Model:   "main-model",
					Message: ollama.Message{Role: "assistant", Thinking: fmt.Sprintf("considering approach %d", call)},
				})
				streamChunk(w, ollama.ChatResponse{Model: "main-model", Done: true, DoneReason: "stop"})
			default:
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		workspace := t.TempDir()
		cfg := config.Config{
			OllamaBaseURL:      server.URL,
			OllamaDefaultModel: "main-model",
			Workspace:          workspace,
			SessionsPath:       filepath.Join(workspace, "sessions"),
			MemoryPath:         filepath.Join(workspace, "memory"),
			SkillsPath:         filepath.Join(workspace, "skills"),
		}
		store := sessions.NewStore(cfg.SessionsPath)
		const sessionID = "thinking-loop"
		if err := store.Save(sessions.Session{ID: sessionID, Title: "Loop", Model: cfg.OllamaDefaultModel}); err != nil {
			t.Fatalf("save session: %v", err)
		}

		result, err := ProcessTurn(context.Background(), Deps{
			ConfigMgr:    config.NewManager(cfg),
			Client:       ollama.NewClient(server.URL),
			SessionStore: store,
		}, TurnRequest{
			SessionID: sessionID,
			Channel:   "web",
			Messages:  []router.MediaMessage{{Message: ollama.Message{Role: "user", Content: "cuál es el pronóstico"}}},
		})
		return result.FinalAnswer, err, atomic.LoadInt32(&chatCalls)
	}

	t.Run("recovery call delivers the answer", func(t *testing.T) {
		answer, err, calls := runTurn(t, "Esta semana estará soleado.")
		if err != nil {
			t.Fatalf("ProcessTurn: %v", err)
		}
		if answer != "Esta semana estará soleado." {
			t.Fatalf("final answer = %q, want the recovered text", answer)
		}
		if calls != 5 {
			t.Fatalf("chat calls = %d, want 5 (4 thinking-only rounds + 1 recovery)", calls)
		}
	})

	t.Run("failed recovery degrades to a graceful message", func(t *testing.T) {
		answer, err, _ := runTurn(t, "")
		if err != nil {
			t.Fatalf("ProcessTurn: %v", err)
		}
		if strings.TrimSpace(answer) == "" {
			t.Fatalf("final answer is empty, want a graceful fallback message")
		}
		if strings.Contains(answer, "empty response") {
			t.Fatalf("final answer leaks the internal error: %q", answer)
		}
	})
}
