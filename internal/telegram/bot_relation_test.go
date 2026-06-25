package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestCheckMessagesRelationship(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
		expected      bool
	}{
		{
			name:          "related response JSON true",
			modelResponse: `{"related": true}`,
			expected:      true,
		},
		{
			name:          "unrelated response JSON false",
			modelResponse: `{"related": false}`,
			expected:      false,
		},
		{
			name:          "related response fallback yes",
			modelResponse: "Yes, they are related.",
			expected:      true,
		},
		{
			name:          "related response fallback yes lowercase",
			modelResponse: "yes",
			expected:      true,
		},
		{
			name:          "unrelated response fallback no",
			modelResponse: "no",
			expected:      false,
		},
		{
			name:          "unrelated response fallback other word",
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
			bot := NewBot(config.NewManager(cfg), client)

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

func TestAutoGenerateSessionTitle(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
		expectedTitle string
	}{
		{
			name:          "JSON title",
			modelResponse: `{"title": "Go Concurrency Model"}`,
			expectedTitle: "Go Concurrency Model",
		},
		{
			name:          "raw text title fallback",
			modelResponse: "Intro to Go Programming",
			expectedTitle: "Intro to Go Programming",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			tempDir := t.TempDir()
			cfg := config.Config{
				OllamaBaseURL:      server.URL,
				OllamaDefaultModel: "test-model",
				SessionsPath:       tempDir,
			}
			client := ollama.NewClient(server.URL)
			sessID := "test-session-id"
			sess := sessions.Session{
				ID:    sessID,
				Title: "New session",
			}
			store := sessions.NewStore(tempDir)
			_ = store.Save(sess)

			if err := engine.AutoNameSession(context.Background(), cfg, client, store, sessID, "Some assistant content."); err != nil {
				t.Fatalf("AutoNameSession failed: %v", err)
			}

			updated, err := store.Get(sessID)
			if err != nil {
				t.Fatalf("failed to retrieve updated session: %v", err)
			}

			if updated.Title != tt.expectedTitle {
				t.Errorf("expected title to be %q, got %q", tt.expectedTitle, updated.Title)
			}
		})
	}
}
