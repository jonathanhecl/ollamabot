package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// Registry holds available tool definitions and their handlers.
type Registry struct {
	enabled     map[string]bool
	defs        []ollama.Tool
	workspace   string
	memoryStore *memory.Store
	client      *ollama.Client
	embedModel  string
}

// NewRegistry creates a registry with the given feature toggles.
func NewRegistry(webSearch bool, workspace string, memoryStore *memory.Store, client *ollama.Client, embedModel string) *Registry {
	r := &Registry{enabled: map[string]bool{}, workspace: workspace, memoryStore: memoryStore, client: client, embedModel: embedModel}
	if webSearch {
		r.enabled["web_search"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "web_search",
				Description: "Search the web using DuckDuckGo to find current information. Returns a list of search result page titles, URLs, and snippet summaries. Always use this first to discover URLs for recent topics.",
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
		r.enabled["fetch_webpage"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "fetch_webpage",
				Description: "Fetch and read the raw text content of a specific webpage URL. Use this to dive deeper into search result links, articles, or documentation to extract detailed answers.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "The absolute HTTP/HTTPS URL of the webpage to fetch.",
						},
					},
					"required": []string{"url"},
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
	if r.memoryStore != nil && r.embedModel != "" {
		r.enabled["memory_search"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "memory_search",
				Description: "Search the long-term memory store using semantic similarity. Use this when the user asks about past conversations, previously discussed topics, or stored knowledge that may not be in the current conversation history.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The search query describing what to recall from memory.",
						},
						"top_k": map[string]any{
							"type":        "integer",
							"description": "Maximum number of memory entries to return (1-10).",
							"default":     3,
						},
					},
					"required": []string{"query"},
				},
			},
		})
		r.enabled["memory_add"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "memory_add",
				Description: "Store a piece of text into long-term memory for future retrieval. Use this to persist important facts, decisions, or context from the current conversation.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "The text to store in memory.",
						},
						"source": map[string]any{
							"type":        "string",
							"description": "Optional source tag (e.g., 'user_fact', 'decision', 'project_note').",
						},
					},
					"required": []string{"text"},
				},
			},
		})
		r.enabled["memory_delete"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "memory_delete",
				Description: "Delete a memory entry by ID. Use this when information becomes outdated, incorrect, or no longer relevant.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "The ID of the memory entry to delete.",
						},
					},
					"required": []string{"id"},
				},
			},
		})
		r.enabled["memory_list"] = true
		r.defs = append(r.defs, ollama.Tool{
			Type: "function",
			Function: ollama.ToolDefinition{
				Name:        "memory_list",
				Description: "List recent memory entries. Use this to review what is stored before deciding whether to add, update, or delete information.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of entries to return (default 20).",
							"default":     20,
						},
					},
				},
			},
		})
	}
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
	case "fetch_webpage":
		urlVal, _ := args["url"].(string)
		if urlVal == "" {
			return "", fmt.Errorf("missing url")
		}
		return Fetch(ctx, urlVal)
	case "read_file":
		path, _ := args["path"].(string)
		if path == "" {
			return "", fmt.Errorf("missing path")
		}
		return ReadFile(r.workspace, path)
	case "memory_search":
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("missing query")
		}
		topK := 3
		if v, ok := args["top_k"]; ok {
			switch n := v.(type) {
			case float64:
				topK = int(n)
			case int:
				topK = n
			case int64:
				topK = int(n)
			}
		}
		resp, err := r.client.Embed(ctx, ollama.EmbedRequest{Model: r.embedModel, Input: query})
		if err != nil {
			return "", fmt.Errorf("embed failed: %w", err)
		}
		if len(resp.Embeddings) == 0 {
			return "", fmt.Errorf("empty embedding response")
		}
		results := r.memoryStore.Search(resp.Embeddings[0], topK)
		if len(results) == 0 {
			return "No relevant memories found.", nil
		}
		var sb strings.Builder
		for i, res := range results {
			fmt.Fprintf(&sb, "[%d] (relevance %.2f) %s\n", i+1, res.Score, res.Text)
		}
		return sb.String(), nil
	case "memory_add":
		text, _ := args["text"].(string)
		if text == "" {
			return "", fmt.Errorf("missing text")
		}
		source, _ := args["source"].(string)
		resp, err := r.client.Embed(ctx, ollama.EmbedRequest{Model: r.embedModel, Input: text})
		if err != nil {
			return "", fmt.Errorf("embed failed: %w", err)
		}
		if len(resp.Embeddings) == 0 {
			return "", fmt.Errorf("empty embedding response")
		}
		entry := memory.Entry{Text: text, Source: source, Embedding: resp.Embeddings[0]}
		if err := r.memoryStore.Add(entry); err != nil {
			return "", fmt.Errorf("store failed: %w", err)
		}
		return fmt.Sprintf("Stored in memory with ID: %s", entry.ID), nil
	case "memory_delete":
		id, _ := args["id"].(string)
		if id == "" {
			return "", fmt.Errorf("missing id")
		}
		if err := r.memoryStore.Delete(id); err != nil {
			return "", fmt.Errorf("delete failed: %w", err)
		}
		return fmt.Sprintf("Deleted memory entry %s", id), nil
	case "memory_list":
		limit := 20
		if v, ok := args["limit"]; ok {
			switch n := v.(type) {
			case float64:
				limit = int(n)
			case int:
				limit = n
			case int64:
				limit = int(n)
			}
		}
		entries := r.memoryStore.List()
		if len(entries) == 0 {
			return "No memory entries stored.", nil
		}
		if limit > len(entries) {
			limit = len(entries)
		}
		var sb strings.Builder
		for i, e := range entries[:limit] {
			fmt.Fprintf(&sb, "[%d] ID: %s | Source: %s | Created: %s | Text: %s\n", i+1, e.ID, e.Source, e.CreatedAt.Format(time.RFC3339), e.Text)
		}
		return sb.String(), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
