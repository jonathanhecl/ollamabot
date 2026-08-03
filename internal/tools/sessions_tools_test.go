package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestSessionsTools(t *testing.T) {
	tempDir := t.TempDir()
	store := sessions.NewStore(tempDir)

	// Create Session 1 (recent)
	s1ID := sessions.GenerateID()
	s1UserMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "user",
		Content:   "Let's discuss the OllamaBot architecture and MCP tools.",
		Timestamp: time.Now().Add(-2 * 24 * time.Hour).Format(time.RFC3339),
	})
	s1AssistantMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "assistant",
		Content:   "OllamaBot uses a modular engine with tool integration.",
		Timestamp: time.Now().Add(-2 * 24 * time.Hour).Format(time.RFC3339),
	})
	s1 := sessions.Session{
		ID:            s1ID,
		Title:         "OllamaBot Architecture Chat",
		Model:         "llama3.1",
		Messages:      []json.RawMessage{s1UserMsg, s1AssistantMsg},
		CreatedAt:     time.Now().Add(-2 * 24 * time.Hour),
		UpdatedAt:     time.Now().Add(-2 * 24 * time.Hour),
		LastMessageAt: time.Now().Add(-2 * 24 * time.Hour),
	}
	if err := store.Save(s1); err != nil {
		t.Fatalf("failed to save s1: %v", err)
	}

	// Create Session 2 (old)
	s2ID := sessions.GenerateID()
	s2UserMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "user",
		Content:   "What recipe do you recommend for dinner?",
		Timestamp: time.Now().Add(-14 * 24 * time.Hour).Format(time.RFC3339),
	})
	s2AssistantMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "assistant",
		Content:   "I recommend a fresh pasta with pesto.",
		Timestamp: time.Now().Add(-14 * 24 * time.Hour).Format(time.RFC3339),
	})
	s2 := sessions.Session{
		ID:            s2ID,
		Title:         "Dinner Recipes",
		Model:         "llama3.1",
		Messages:      []json.RawMessage{s2UserMsg, s2AssistantMsg},
		CreatedAt:     time.Now().Add(-14 * 24 * time.Hour),
		UpdatedAt:     time.Now().Add(-14 * 24 * time.Hour),
		LastMessageAt: time.Now().Add(-14 * 24 * time.Hour),
	}
	if err := store.Save(s2); err != nil {
		t.Fatalf("failed to save s2: %v", err)
	}

	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSessionStore(store)

	ctx := context.Background()

	execTool := func(name string, params map[string]any) (string, error) {
		argsBytes, _ := json.Marshal(params)
		return registry.Execute(ctx, ollama.ToolCall{
			Type: "function",
			Function: ollama.ToolFunction{
				Name:      name,
				Arguments: argsBytes,
			},
		})
	}

	// 1. Test sessions_list with since_days filter
	t.Run("sessions_list_since_days", func(t *testing.T) {
		out, err := execTool("sessions_list", map[string]any{
			"since_days": 7,
		})
		if err != nil {
			t.Fatalf("sessions_list error: %v", err)
		}
		if !strings.Contains(out, s1ID) {
			t.Errorf("expected recent session %s in output, got:\n%s", s1ID, out)
		}
		if strings.Contains(out, s2ID) {
			t.Errorf("did not expect old session %s in output due to since_days limit, got:\n%s", s2ID, out)
		}
	})

	// 2. Test sessions_list with query filter
	t.Run("sessions_list_query", func(t *testing.T) {
		out, err := execTool("sessions_list", map[string]any{
			"query": "Dinner",
		})
		if err != nil {
			t.Fatalf("sessions_list error: %v", err)
		}
		if !strings.Contains(out, s2ID) {
			t.Errorf("expected session %s in query results, got:\n%s", s2ID, out)
		}
		if strings.Contains(out, s1ID) {
			t.Errorf("did not expect session %s in query results, got:\n%s", s1ID, out)
		}
	})

	// 3. Test sessions_search across message content
	t.Run("sessions_search", func(t *testing.T) {
		out, err := execTool("sessions_search", map[string]any{
			"query": "architecture",
		})
		if err != nil {
			t.Fatalf("sessions_search error: %v", err)
		}
		if !strings.Contains(out, s1ID) || !strings.Contains(out, "OllamaBot architecture") {
			t.Errorf("expected search match for architecture in s1, got:\n%s", out)
		}
	})

	// 4. Test session_read
	t.Run("session_read", func(t *testing.T) {
		out, err := execTool("session_read", map[string]any{
			"session_id": s1ID,
		})
		if err != nil {
			t.Fatalf("session_read error: %v", err)
		}
		if !strings.Contains(out, s1ID) || !strings.Contains(out, "[USER]") || !strings.Contains(out, "[ASSISTANT]") {
			t.Errorf("expected formatted session transcript, got:\n%s", out)
		}
	})
}
