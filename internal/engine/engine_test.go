package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
)

func TestProcessTurnUsesConfiguredMainModel(t *testing.T) {
	var requestedModel string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			var req ollama.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestedModel = req.Model
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Message: ollama.Message{Role: "assistant", Content: "ok"},
				Done:    true,
			})
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				Capabilities: []string{"completion", "tools"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	cfg := config.Config{
		OllamaBaseURL:      ts.URL,
		OllamaDefaultModel: "configured-main",
		Workspace:          t.TempDir(),
		SessionsPath:       t.TempDir(),
		MemoryPath:         t.TempDir(),
		SkillsPath:         t.TempDir(),
	}
	client := ollama.NewClient(ts.URL)

	result, err := ProcessTurn(context.Background(), Deps{
		Config: cfg,
		Client: client,
	}, TurnRequest{
		Channel: "web",
		Messages: []router.MediaMessage{
			{Message: ollama.Message{Role: "user", Content: "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("ProcessTurn failed: %v", err)
	}
	if result.ModelUsed != "configured-main" {
		t.Fatalf("ModelUsed = %q, want configured-main", result.ModelUsed)
	}
	if requestedModel != "configured-main" {
		t.Fatalf("Ollama request model = %q, want configured-main", requestedModel)
	}
}
