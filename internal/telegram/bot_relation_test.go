package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestCheckMessagesRelationship(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
		expected      bool
	}{
		{
			name:          "related response yes",
			modelResponse: "Yes, they are related.",
			expected:      true,
		},
		{
			name:          "related response contains yes lowercase",
			modelResponse: "yes",
			expected:      true,
		},
		{
			name:          "unrelated response no",
			modelResponse: "no",
			expected:      false,
		},
		{
			name:          "unrelated response other word",
			modelResponse: "completely different topic",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Spin up local mock server for Ollama
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					t.Errorf("expected POST /api/chat, got %s", r.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
				resp := ollama.ChatResponse{
					Message: ollama.Message{
						Role:    "assistant",
						Content: tt.modelResponse,
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			cfg := config.Config{
				OllamaBaseURL:      server.URL,
				OllamaDefaultModel: "test-model",
			}
			client := ollama.NewClient(server.URL)
			bot := NewBot(cfg, client)

			history := []rawMsg{
				{Role: "user", Content: "What is Go?"},
				{Role: "assistant", Content: "Go is a programming language designed at Google."},
			}
			newMessage := "Tell me more about its concurrency model."

			got := bot.checkMessagesRelationship(context.Background(), history, newMessage)
			if got != tt.expected {
				t.Errorf("checkMessagesRelationship() = %v, want %v (model response was %q)", got, tt.expected, tt.modelResponse)
			}
		})
	}
}
