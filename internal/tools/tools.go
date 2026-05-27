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
	todoStore   *TodoStore
}

// NewRegistry creates a registry with the given feature toggles.
func NewRegistry(webSearch bool, workspace string, memoryStore *memory.Store, client *ollama.Client, embedModel string) *Registry {
	r := &Registry{
		enabled:     map[string]bool{},
		workspace:   workspace,
		memoryStore: memoryStore,
		client:      client,
		embedModel:  embedModel,
		todoStore:   NewTodoStore(),
	}

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
			Description: "Read the contents of a text file within the workspace safely. Returns the file content or lists directory entries if path points to a directory.",
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

	// Register Write Tool
	r.enabled["Write"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "Write",
			Description: "Write file contents atomically to a path in the workspace. Overwrites existing files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to write the file, relative to the workspace.",
					},
					"contents": map[string]any{
						"type":        "string",
						"description": "The full code or text contents to write.",
					},
				},
				"required": []string{"file_path", "contents"},
			},
		},
	})

	// Register Edit Tool
	r.enabled["Edit"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "Edit",
			Description: "Edit existing file content by replacing an exact old string with a new string.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to the file to edit, relative to the workspace.",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The exact string block in the file to replace. Must match exactly including indentation.",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The replacement string.",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "If true, replaces all occurrences of old_string. Otherwise errors if duplicate blocks exist.",
					},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
	})

	// Register TodoWrite Tool
	r.enabled["TodoWrite"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "TodoWrite",
			Description: "Maintain a live TODO checklist of steps during this turn. Use it when solving multi-step tasks. Statuses are: 'pending', 'in_progress', 'completed', 'cancelled'.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"merge": map[string]any{
						"type":        "boolean",
						"description": "If true, merges incoming todos into the existing list. Otherwise replaces the entire list.",
					},
					"todos": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":      map[string]any{"type": "string", "description": "Unique short identifier (e.g. 'step-1')."},
								"content": map[string]any{"type": "string", "description": "Short action item text."},
								"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
							},
							"required": []string{"id", "content", "status"},
						},
					},
				},
				"required": []string{"todos"},
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

// TodoStore returns the checklist store instance.
func (r *Registry) TodoStore() *TodoStore {
	return r.todoStore
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
	case "Write":
		filePath, _ := args["file_path"].(string)
		contents, _ := args["contents"].(string)
		if filePath == "" {
			return "", fmt.Errorf("missing file_path")
		}
		err := WriteFile(r.workspace, filePath, contents)
		if err != nil {
			return "", err
		}
		return "Write successful.", nil
	case "Edit":
		filePath, _ := args["file_path"].(string)
		oldString, _ := args["old_string"].(string)
		newString, _ := args["new_string"].(string)
		replaceAll, _ := args["replace_all"].(bool)
		if filePath == "" || oldString == "" {
			return "", fmt.Errorf("missing required edit arguments")
		}
		diff, err := EditFile(r.workspace, filePath, oldString, newString, replaceAll)
		if err != nil {
			return "", err
		}
		if diff == "" {
			return "No changes made.", nil
		}
		return "Edit successful. Changes made:\n" + diff, nil
	case "TodoWrite":
		todosVal := args["todos"]
		merge, _ := args["merge"].(bool)
		if todosVal == nil {
			return "", fmt.Errorf("missing todos")
		}
		items, err := DecodeTodos(todosVal)
		if err != nil {
			return "", fmt.Errorf("failed to decode todos: %w", err)
		}
		if err := ValidateTodoContent(items, merge, r.todoStore.Snapshot()); err != nil {
			return "", fmt.Errorf("invalid todos: %w", err)
		}
		finalList := r.todoStore.Apply(merge, items)
		return RenderTodosForModel(finalList), nil
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
