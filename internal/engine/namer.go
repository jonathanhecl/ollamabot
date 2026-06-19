package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

var titleGenerationSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title": map[string]any{
			"type":        "string",
			"description": "An extremely short title (2 to 4 words) summarizing the main topic of the conversation context, without quotes, punctuation, or explanations.",
		},
	},
	"required": []string{"title"},
}

type titleResponse struct {
	Title string `json:"title"`
}

func GenerateSessionTitle(ctx context.Context, cfg config.Config, client *ollama.Client, text string) (string, error) {
	model := config.ResolveModel(cfg, config.ModelRoleSubagent)
	if strings.TrimSpace(model) == "" {
		return "", nil
	}
	resp, err := client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{
				Role:    "system",
				Content: "Summarize the main topic of the text in an extremely short title (2 to 4 words). Do not use quotation marks, punctuation, or explanations. Respond with a JSON object containing the title.",
			},
			{
				Role:    "user",
				Content: text,
			},
		},
		Format:  titleGenerationSchema,
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return "", err
	}

	rawText := strings.TrimSpace(resp.Message.Content)
	var generatedTitle string
	var parsed titleResponse
	if err := json.Unmarshal([]byte(rawText), &parsed); err == nil {
		generatedTitle = parsed.Title
	} else {
		generatedTitle = rawText
	}
	generatedTitle = strings.TrimSpace(generatedTitle)
	generatedTitle = strings.Trim(generatedTitle, `"'`)
	generatedTitle = strings.TrimRight(generatedTitle, ".!?")
	return generatedTitle, nil
}

func AutoNameSession(ctx context.Context, cfg config.Config, client *ollama.Client, store *sessions.Store, sessionID string, text string) error {
	if store == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	if looksLikeError(text) {
		return nil
	}
	sess, err := store.Get(sessionID)
	if err != nil {
		return err
	}
	if !sessions.IsDefaultTitle(sess.Title) {
		return nil
	}
	title, err := GenerateSessionTitle(ctx, cfg, client, text)
	if err != nil || strings.TrimSpace(title) == "" {
		return err
	}
	sess.Title = title
	if err := store.Save(sess); err != nil {
		return err
	}
	sessions.NotifyUpdate(sessionID)
	return nil
}

func looksLikeError(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "Error:") ||
		strings.Contains(trimmed, "\nError:") ||
		strings.Contains(strings.ToLower(trimmed), "model not found")
}
