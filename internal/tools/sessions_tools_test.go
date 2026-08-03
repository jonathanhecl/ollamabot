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

	now := time.Now()
	twoDaysAgo := now.Add(-2 * 24 * time.Hour)
	fourteenDaysAgo := now.Add(-14 * 24 * time.Hour)

	// Create Session 1 (recent)
	s1ID := sessions.GenerateID()
	s1UserMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "user",
		Content:   "Let's discuss the OllamaBot architecture and MCP tools.",
		Timestamp: twoDaysAgo.Format(time.RFC3339),
	})
	s1AssistantMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "assistant",
		Content:   "OllamaBot uses a modular engine with tool integration.",
		Timestamp: twoDaysAgo.Format(time.RFC3339),
	})
	s1 := sessions.Session{
		ID:            s1ID,
		Title:         "OllamaBot Architecture Chat",
		Model:         "llama3.1",
		Messages:      []json.RawMessage{s1UserMsg, s1AssistantMsg},
		CreatedAt:     twoDaysAgo,
		UpdatedAt:     twoDaysAgo,
		LastMessageAt: twoDaysAgo,
	}
	if err := store.Save(s1); err != nil {
		t.Fatalf("failed to save s1: %v", err)
	}

	// Create Session 2 (old, untitled with default title)
	s2ID := sessions.GenerateID()
	s2UserMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "user",
		Content:   "What recipe do you recommend for dinner tonight?",
		Timestamp: fourteenDaysAgo.Format(time.RFC3339),
	})
	s2AssistantMsg, _ := json.Marshal(sessions.RawMsg{
		Role:      "assistant",
		Content:   "I recommend a fresh pasta with pesto.",
		Timestamp: fourteenDaysAgo.Format(time.RFC3339),
	})
	s2 := sessions.Session{
		ID:            s2ID,
		Title:         "New session",
		Model:         "llama3.1",
		Messages:      []json.RawMessage{s2UserMsg, s2AssistantMsg},
		CreatedAt:     fourteenDaysAgo,
		UpdatedAt:     fourteenDaysAgo,
		LastMessageAt: fourteenDaysAgo,
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

	// 2. Test sessions_list with date_from / date_to YYYY-MM-DD filter
	t.Run("sessions_list_date_range", func(t *testing.T) {
		dateStr := fourteenDaysAgo.Format("2006-01-02")
		out, err := execTool("sessions_list", map[string]any{
			"date_from": dateStr,
			"date_to":   dateStr,
		})
		if err != nil {
			t.Fatalf("sessions_list error: %v", err)
		}
		if !strings.Contains(out, s2ID) {
			t.Errorf("expected session %s for date %s, got:\n%s", s2ID, dateStr, out)
		}
		if strings.Contains(out, s1ID) {
			t.Errorf("did not expect session %s for date %s, got:\n%s", s1ID, dateStr, out)
		}
	})

	// 3. Test sessions_list preview snippet for untitled session
	t.Run("sessions_list_untitled_preview", func(t *testing.T) {
		out, err := execTool("sessions_list", map[string]any{})
		if err != nil {
			t.Fatalf("sessions_list error: %v", err)
		}
		if !strings.Contains(out, "Preview: \"What recipe do you recommend") {
			t.Errorf("expected preview snippet for untitled session %s, got:\n%s", s2ID, out)
		}
	})

	// 4. Test sessions_search with multi-word tokenized query
	t.Run("sessions_search_multi_token", func(t *testing.T) {
		out, err := execTool("sessions_search", map[string]any{
			"query": "recipe dinner",
		})
		if err != nil {
			t.Fatalf("sessions_search error: %v", err)
		}
		if !strings.Contains(out, s2ID) || !strings.Contains(out, "What recipe do you recommend") {
			t.Errorf("expected multi-token match for 'recipe dinner' in s2, got:\n%s", out)
		}
	})

	// 5. Test session_read
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

	// 6. Test sessions_digest
	t.Run("sessions_digest", func(t *testing.T) {
		out, err := execTool("sessions_digest", map[string]any{
			"since_days": 30,
		})
		if err != nil {
			t.Fatalf("sessions_digest error: %v", err)
		}
		if !strings.Contains(out, "Digest") || !strings.Contains(out, s1ID) {
			t.Errorf("expected executive session digest containing s1ID, got:\n%s", out)
		}
	})

	// 7. Test session_export
	t.Run("session_export", func(t *testing.T) {
		out, err := execTool("session_export", map[string]any{
			"session_id":  s1ID,
			"output_path": "exports/test_export.md",
		})
		if err != nil {
			t.Fatalf("session_export error: %v", err)
		}
		if !strings.Contains(out, "successfully exported") {
			t.Errorf("expected success message for session_export, got:\n%s", out)
		}
	})
}
