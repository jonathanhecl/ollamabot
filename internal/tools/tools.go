package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// Registry holds available tool definitions and their handlers.
type Registry struct {
	enabled   map[string]bool
	defs      []ollama.Tool
	workspace string
}

// NewRegistry creates a registry with the given feature toggles.
func NewRegistry(webSearch bool, workspace string) *Registry {
	r := &Registry{enabled: map[string]bool{}, workspace: workspace}
	if webSearch {
		r.enabled["web_search"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "web_search",
				Description: "Search the web using DuckDuckGo to find current information. Use this when the user asks about recent events, facts that may have changed, or topics outside your training data.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The search query.",
						},
						"max_results": map[string]any{
							"type":        "integer",
							"description": "Maximum number of results to return (1-10).",
							"default":     5,
						},
					},
					"required": []string{"query"},
				},
			},
		})
	}
	r.enabled["read_file"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "read_file",
			Description: "Read the contents of a text file within the workspace. Returns the file content or an error if the file is missing, too large, or binary. If the path is a directory, it lists the entries.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative path within the workspace.",
					},
				},
				"required": []string{"path"},
			},
		},
	})
	return r
}

// Definitions returns the Ollama tool definitions to expose to the model.
func (r *Registry) Definitions() []ollama.Tool {
	return r.defs
}

// Execute runs a tool call and returns the result string.
func (r *Registry) Execute(ctx context.Context, call ollama.ToolCall) (string, error) {
	name := call.Function.Name
	if !r.enabled[name] {
		return "", fmt.Errorf("tool %q is not enabled", name)
	}

	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	start := time.Now()
	result, err := r.execute(ctx, name, args)
	duration := time.Since(start)
	if err != nil {
		log.Printf("[tool] %s error (%v): %v", name, duration, err)
		return "", err
	}
	log.Printf("[tool] %s ok (%v) result_len=%d", name, duration, len(result))
	return result, nil
}

func (r *Registry) execute(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	case "web_search":
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("missing query")
		}
		maxResults := 5
		if v, ok := args["max_results"]; ok {
			switch n := v.(type) {
			case float64:
				maxResults = int(n)
			case int:
				maxResults = n
			case int64:
				maxResults = int(n)
			}
		}
		return Search(ctx, query, maxResults)
	case "read_file":
		path, _ := args["path"].(string)
		if path == "" {
			return "", fmt.Errorf("missing path")
		}
		return ReadFile(r.workspace, path)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
