package probe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type Runner struct {
	client *ollama.Client
}

type Result struct {
	Name    string
	Model   string
	Status  capabilities.Status
	Details string
}

func NewRunner(client *ollama.Client) *Runner {
	return &Runner{client: client}
}

func (r *Runner) Inventory(ctx context.Context, only []string) ([]capabilities.ModelReport, error) {
	tags, err := r.client.Tags(ctx)
	if err != nil {
		return nil, err
	}
	filter := map[string]bool{}
	for _, model := range only {
		filter[model] = true
	}

	var reports []capabilities.ModelReport
	for _, tag := range tags.Models {
		if len(filter) > 0 && !filter[tag.Name] && !filter[tag.Model] {
			continue
		}
		show, err := r.client.Show(ctx, tag.Name)
		if err != nil {
			return nil, fmt.Errorf("show %s: %w", tag.Name, err)
		}
		reports = append(reports, capabilities.FromOllama(tag, show))
	}
	return reports, nil
}

func (r *Runner) Chat(ctx context.Context, model string) (Result, error) {
	response, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: "Reply with exactly: ok"},
		},
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return result("chat", model, capabilities.Pending, err.Error()), err
	}
	if strings.TrimSpace(response.Message.Content) == "" {
		return result("chat", model, capabilities.Pending, "empty assistant response"), nil
	}
	return result("chat", model, capabilities.Confirmed, response.Message.Content), nil
}

func (r *Runner) Tools(ctx context.Context, model string) (Result, error) {
	tool := temperatureTool()
	messages := []ollama.Message{
		{Role: "user", Content: "What is the temperature in Tokyo? Use the provided tool."},
	}
	first, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Tools:    []ollama.Tool{tool},
		Think:    true,
	})
	if err != nil {
		return result("tools", model, capabilities.Pending, err.Error()), err
	}
	if len(first.Message.ToolCalls) == 0 {
		return result("tools", model, capabilities.Pending, "model did not return tool_calls"), nil
	}
	messages = append(messages, first.Message)
	for _, call := range first.Message.ToolCalls {
		if call.Function.Name != "get_temperature" {
			continue
		}
		messages = append(messages, ollama.Message{
			Role:    "tool",
			Name:    call.Function.Name,
			Content: "18C",
		})
	}
	final, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Tools:    []ollama.Tool{tool},
		Think:    true,
	})
	if err != nil {
		return result("tools", model, capabilities.Pending, err.Error()), err
	}
	return result("tools", model, capabilities.Confirmed, strings.TrimSpace(final.Message.Content)), nil
}

func (r *Runner) JSON(ctx context.Context, model string) (Result, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"ok":   map[string]any{"type": "boolean"},
		},
		"required": []string{"name", "ok"},
	}
	response, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: "Return JSON for a probe named ollamabot with ok true."},
		},
		Format:  schema,
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return result("json", model, capabilities.Pending, err.Error()), err
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(response.Message.Content), &decoded); err != nil {
		return result("json", model, capabilities.Pending, "invalid JSON: "+err.Error()), nil
	}
	return result("json", model, capabilities.Confirmed, response.Message.Content), nil
}

func (r *Runner) Vision(ctx context.Context, model, imagePath string) (Result, error) {
	payload, err := os.ReadFile(imagePath)
	if err != nil {
		return result("vision", model, capabilities.Pending, err.Error()), err
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	response, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: "Describe this image in one short sentence.", Images: []string{encoded}},
		},
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return result("vision", model, capabilities.Pending, err.Error()), err
	}
	if strings.TrimSpace(response.Message.Content) == "" {
		return result("vision", model, capabilities.Pending, "empty vision response"), nil
	}
	return result("vision", model, capabilities.Confirmed, response.Message.Content), nil
}

func (r *Runner) Thinking(ctx context.Context, model string) (Result, error) {
	response, err := r.client.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: "How many r letters are in strawberry? Answer briefly."},
		},
		Think:   true,
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return result("thinking", model, capabilities.Pending, err.Error()), err
	}
	if strings.TrimSpace(response.Message.Thinking) != "" {
		return result("thinking", model, capabilities.Confirmed, "message.thinking returned"), nil
	}
	show, err := r.client.Show(ctx, model)
	if err == nil {
		for _, capability := range show.Capabilities {
			if capability == "thinking" {
				return result("thinking", model, capabilities.Confirmed, "model reports thinking capability"), nil
			}
		}
	}
	return result("thinking", model, capabilities.Pending, "no thinking field returned"), nil
}

func (r *Runner) Embeddings(ctx context.Context, model string) (Result, error) {
	response, err := r.client.Embed(ctx, ollama.EmbedRequest{
		Model: model,
		Input: "The quick brown fox jumps over the lazy dog.",
	})
	if err != nil {
		return result("embeddings", model, capabilities.Pending, err.Error()), err
	}
	if len(response.Embeddings) == 0 || len(response.Embeddings[0]) == 0 {
		return result("embeddings", model, capabilities.Pending, "empty embedding vector"), nil
	}
	return result("embeddings", model, capabilities.Confirmed, fmt.Sprintf("vector length %d", len(response.Embeddings[0]))), nil
}

func (r *Runner) Audio(ctx context.Context, model string, audioPath string) (Result, error) {
	if strings.TrimSpace(audioPath) != "" {
		payload, err := os.ReadFile(audioPath)
		if err != nil {
			return result("audio", model, capabilities.Pending, err.Error()), err
		}
		encoded := base64.StdEncoding.EncodeToString(payload)
		response, err := r.client.Chat(ctx, ollama.ChatRequest{
			Model: model,
			Messages: []ollama.Message{
				{Role: "user", Content: "Describe what you hear in one short sentence.", Images: []string{encoded}},
			},
			Options: map[string]any{"temperature": 0, "num_ctx": 8000},
		})
		if err != nil {
			return result("audio", model, capabilities.Pending, err.Error()), err
		}
		if strings.TrimSpace(response.Message.Content) == "" {
			return result("audio", model, capabilities.Pending, "empty audio response"), nil
		}
		return result("audio", model, capabilities.Confirmed, response.Message.Content), nil
	}

	show, err := r.client.Show(ctx, model)
	if err != nil {
		return result("audio", model, capabilities.Pending, err.Error()), err
	}
	for _, capability := range show.Capabilities {
		if capability == "audio" {
			return result("audio", model, capabilities.Confirmed, "model reports audio capability; pass --audio PATH for end-to-end validation"), nil
		}
	}
	if hasAudio, _ := show.ProjectorInfo["clip.has_audio_encoder"].(bool); hasAudio {
		return result("audio", model, capabilities.Inferred, "audio encoder detected in projector_info; REST payload remains unconfirmed"), nil
	}
	return result("audio", model, capabilities.Pending, "no audio encoder detected and no stable REST audio payload documented"), nil
}

func result(name, model string, status capabilities.Status, details string) Result {
	return Result{Name: name, Model: model, Status: status, Details: details}
}

func temperatureTool() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "get_temperature",
			Description: "Get the current temperature for a city",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"city"},
				"properties": map[string]any{
					"city": map[string]any{"type": "string", "description": "The city name"},
				},
			},
		},
	}
}
