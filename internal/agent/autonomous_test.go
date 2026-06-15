package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestAutonomousManager_GenerateInitialTodos(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
		expectedTasks []string
		wantErr       bool
	}{
		{
			name:          "valid JSON schema matching todos object",
			modelResponse: `{"todos": [{"id": "task-1", "content": "Setup HTML foundation"}, {"id": "task-2", "content": "Style components"}]}`,
			expectedTasks: []string{"Setup HTML foundation", "Style components"},
			wantErr:       false,
		},
		{
			name:          "fallback legacy JSON array",
			modelResponse: `[{"id": "task-1", "content": "Setup HTML foundation"}, {"id": "task-2", "content": "Style components"}]`,
			expectedTasks: []string{"Setup HTML foundation", "Style components"},
			wantErr:       false,
		},
		{
			name:          "invalid response text",
			modelResponse: "Invalid response content",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				resp := ollama.ChatResponse{
					Message: ollama.Message{
						Role:    "assistant",
						Content: tt.modelResponse,
					},
					Done: true,
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			cfg := config.Config{
				OllamaDefaultModel: "test-model",
			}
			client := ollama.NewClient(server.URL)
			am := NewAutonomousManager(cfg, client, nil)

			todos, err := am.generateInitialTodos(context.Background(), "test-project", "some goal")
			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error: %v, got: %v", tt.wantErr, err)
			}
			if !tt.wantErr {
				if len(todos) != len(tt.expectedTasks) {
					t.Fatalf("expected %d todos, got %d", len(tt.expectedTasks), len(todos))
				}
				for i, task := range todos {
					if task.Content != tt.expectedTasks[i] {
						t.Errorf("todo %d expected content %q, got %q", i, tt.expectedTasks[i], task.Content)
					}
					if task.Status != "pending" {
						t.Errorf("todo %d expected pending status, got %q", i, task.Status)
					}
				}
			}
		})
	}
}
