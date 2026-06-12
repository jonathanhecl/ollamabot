package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// ApprovalHandler is implemented by clients wishing to approve or deny execution of risky tools.
type ApprovalHandler interface {
	RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error)
}

// ClarificationHandler is implemented by clients wishing to ask clarifying questions to the user.
type ClarificationHandler interface {
	RequestClarification(ctx context.Context, question string, options []string) (string, error)
}

// Registry holds available tool definitions and their handlers.
type Registry struct {
	enabled              map[string]bool
	defs                 []ollama.Tool
	workspace            string
	memoryStore          *memory.Store
	client               *ollama.Client
	embedModel           string
	todoStore            *TodoStore
	approvalHandler      ApprovalHandler
	clarificationHandler ClarificationHandler
	skillsPath           string
	searchCfg            SearchConfig
	imageModel           string
	imageSteps           int
	imageProgressHandler ImageProgressHandler
}

// ImageProgressHandler is called during image generation with progress updates
type ImageProgressHandler interface {
	OnProgress(completed, total int, message string)
	OnComplete(imagePath string)
	SetGenerationID(id string)
}

// SetApprovalHandler assigns a callback handler to approve risky tools.
func (r *Registry) SetApprovalHandler(h ApprovalHandler) {
	r.approvalHandler = h
}

// SetClarificationHandler assigns a callback handler to ask clarification questions.
func (r *Registry) SetClarificationHandler(h ClarificationHandler) {
	r.clarificationHandler = h
}

// SetSkillsPath assigns the skills directory path.
func (r *Registry) SetSkillsPath(p string) {
	r.skillsPath = p
}

// SetImageModel assigns the image generation model.
func (r *Registry) SetImageModel(model string) {
	r.imageModel = model
}

// SetImageSteps assigns the image generation steps.
func (r *Registry) SetImageSteps(steps int) {
	r.imageSteps = steps
}

// SetImageProgressHandler assigns a callback handler for image generation progress updates.
func (r *Registry) SetImageProgressHandler(h ImageProgressHandler) {
	r.imageProgressHandler = h
}

// NewRegistry creates a registry with the given feature toggles.
func NewRegistry(webSearch bool, workspace string, memoryStore *memory.Store, client *ollama.Client, embedModel string, searchCfg SearchConfig) *Registry {
	r := &Registry{
		enabled:     map[string]bool{},
		workspace:   workspace,
		memoryStore: memoryStore,
		client:      client,
		embedModel:  embedModel,
		todoStore:   NewTodoStore(),
		skillsPath:  "skills",
		searchCfg:   searchCfg,
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

	// Register Skill Management Tools
	r.enabled["skill_list"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "skill_list",
			Description: "List all custom skills currently installed in the system.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	})

	r.enabled["skill_get"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "skill_get",
			Description: "Retrieve the raw instruction contents of a specific skill.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the skill to fetch.",
					},
				},
				"required": []string{"name"},
			},
		},
	})

	r.enabled["skill_create"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "skill_create",
			Description: "Create a new custom skill with frontmatter and checklist instructions.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Unique short name (alphanumeric/dashes) for the skill folder.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Short explanation of the workflow this skill implements.",
					},
					"homepage": map[string]any{
						"type":        "string",
						"description": "Author homepage or reference URL.",
					},
					"instructions": map[string]any{
						"type":        "string",
						"description": "Step-by-step checklist instructions (lines starting with - [ ] or similar).",
					},
				},
				"required": []string{"name", "description", "homepage", "instructions"},
			},
		},
	})

	r.enabled["skill_edit"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "skill_edit",
			Description: "Modify or merge properties of an existing custom skill.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the skill to modify.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "New description (optional).",
					},
					"homepage": map[string]any{
						"type":        "string",
						"description": "New homepage URL (optional).",
					},
					"instructions": map[string]any{
						"type":        "string",
						"description": "New checklist instructions (optional).",
					},
				},
				"required": []string{"name"},
			},
		},
	})

	r.enabled["skill_delete"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "skill_delete",
			Description: "Remove a custom skill entirely from the system.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the skill to delete.",
					},
				},
				"required": []string{"name"},
			},
		},
	})

	r.enabled["execute_command"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "execute_command",
			Description: "Run a shell command (e.g. ffmpeg, ffprobe, pandoc, python3) inside the workspace directory. Use this to process binary files, extract frames from video, convert formats, etc. Always requires user approval before execution.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": fmt.Sprintf("The executable to run. Allowed: %s", strings.Join(allowedList(), ", ")),
					},
					"args": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "List of arguments to pass to the command.",
					},
				},
				"required": []string{"command"},
			},
		},
	})

	r.enabled["ask_clarification"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "ask_clarification",
			Description: "Ask the user a clarifying question with a list of pre-defined affirmative options (at least 2) to resolve ambiguity in their instruction and plan the next action better.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The clarifying question to ask the user.",
					},
					"options": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "A list of at least 3 affirmative option statements for the user to choose from. Each option must be a statement, not a question.",
					},
				},
				"required": []string{"question", "options"},
			},
		},
	})

	// Register Generate Image Tool (enabled when image model is configured)
	r.enabled["generate_image"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "generate_image",
			Description: "Generate an image using a diffusion model (Flux, etc.). Use this when the user explicitly or implicitly requests image generation (e.g., 'generate an image of...', 'create a picture of...', 'draw...', 'imagine...'). The agent should choose appropriate resolution: 512x512 for square/portrait, 1024x512 for landscape, 512x1024 for tall/portrait. Returns the path to the generated image file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "Detailed description of the image to generate. Be specific about subject, style, lighting, mood, colors.",
					},
					"width": map[string]any{
						"type":        "integer",
						"description": "Image width in pixels. Options: 512 (default), 768, 1024.",
						"enum":        []int{512, 768, 1024},
						"default":     512,
					},
					"height": map[string]any{
						"type":        "integer",
						"description": "Image height in pixels. Options: 512 (default), 768, 1024. Choose based on desired aspect ratio: 1024x512 for landscape, 512x1024 for portrait, 512x512 for square.",
						"enum":        []int{512, 768, 1024},
						"default":     512,
					},
					"seed": map[string]any{
						"type":        "integer",
						"description": "Random seed for reproducibility. Use 0 for random (default).",
						"default":     0,
					},
				},
				"required": []string{"prompt"},
			},
		},
	})

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

// MemoryStore returns the long-term memory store instance.
func (r *Registry) MemoryStore() *memory.Store {
	return r.memoryStore
}

// EmbedModel returns the embedding model name.
func (r *Registry) EmbedModel() string {
	return r.embedModel
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

	// Risks check: write/edit/execute operations require approval if a handler is configured
	if r.approvalHandler != nil && (name == "Write" || name == "Edit" || name == "execute_command") {
		filePath, _ := args["file_path"].(string)
		isSafe := false
		if filePath != "" {
			if absPath, err := ResolveAndValidatePath(r.workspace, filePath); err == nil {
				// Check if the path contains a directory named "workspace" or "agent"
				segments := strings.Split(absPath, string(filepath.Separator))
				for _, seg := range segments {
					lowerSeg := strings.ToLower(seg)
					if lowerSeg == "workspace" || lowerSeg == "agent" {
						isSafe = true
						break
					}
				}
			}
		}

		// execute_command always requires approval regardless of path
		if name == "execute_command" {
			isSafe = false
		}

		if !isSafe {
			log.Printf("[tool] Intercepting risky tool %q. Requesting user approval...", name)
			approved, err := r.approvalHandler.RequestApproval(ctx, name, args)
			if err != nil {
				log.Printf("[tool] Approval error for %q: %v", name, err)
				return "", fmt.Errorf("tool approval failed: %w", err)
			}
			if !approved {
				log.Printf("[tool] Risky tool %q execution DENIED by user", name)
				return "Error: Execution denied by user.", nil
			}
			log.Printf("[tool] Risky tool %q execution APPROVED by user", name)
		} else {
			log.Printf("[tool] Risky tool %q is inside safe workspace. Bypassing approval.", name)
		}
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
		return SearchWithConfig(ctx, r.searchCfg, query, maxResults)
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
		if err := r.memoryStore.Add(&entry); err != nil {
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
	case "skill_list":
		return ListSkills(r.skillsPath)
	case "skill_get":
		skillName, _ := args["name"].(string)
		if skillName == "" {
			return "", fmt.Errorf("missing skill name")
		}
		return GetSkill(r.skillsPath, skillName)
	case "skill_create":
		skillName, _ := args["name"].(string)
		desc, _ := args["description"].(string)
		hp, _ := args["homepage"].(string)
		inst, _ := args["instructions"].(string)
		if skillName == "" || desc == "" || hp == "" || inst == "" {
			return "", fmt.Errorf("missing required arguments for skill_create")
		}
		err := CreateSkill(r.skillsPath, skillName, desc, hp, inst)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Skill '%s' created successfully.", skillName), nil
	case "skill_edit":
		skillName, _ := args["name"].(string)
		desc, _ := args["description"].(string)
		hp, _ := args["homepage"].(string)
		inst, _ := args["instructions"].(string)
		if skillName == "" {
			return "", fmt.Errorf("missing skill name")
		}
		err := EditSkill(r.skillsPath, skillName, desc, hp, inst)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Skill '%s' updated successfully.", skillName), nil
	case "skill_delete":
		skillName, _ := args["name"].(string)
		if skillName == "" {
			return "", fmt.Errorf("missing skill name")
		}
		err := DeleteSkill(r.skillsPath, skillName)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Skill '%s' deleted successfully.", skillName), nil
	case "execute_command":
		command, _ := args["command"].(string)
		if command == "" {
			return "", fmt.Errorf("missing command")
		}
		var cmdArgs []string
		if v, ok := args["args"]; ok {
			argBytes, err := json.Marshal(v)
			if err == nil {
				_ = json.Unmarshal(argBytes, &cmdArgs)
			}
		}
		return executeCommand(ctx, r.workspace, command, cmdArgs)
	case "ask_clarification":
		question, _ := args["question"].(string)
		optionsVal := args["options"]
		if question == "" || optionsVal == nil {
			return "", fmt.Errorf("missing required arguments for ask_clarification")
		}
		var options []string
		bytes, err := json.Marshal(optionsVal)
		if err != nil {
			return "", fmt.Errorf("failed to encode options: %w", err)
		}
		if err := json.Unmarshal(bytes, &options); err != nil {
			return "", fmt.Errorf("failed to decode options: %w", err)
		}
		if len(options) < 2 {
			return "", fmt.Errorf("ask_clarification requires at least 2 options")
		}
		if r.clarificationHandler == nil {
			return "", fmt.Errorf("no clarification handler configured")
		}
		return r.clarificationHandler.RequestClarification(ctx, question, options)
	case "generate_image":
		prompt, _ := args["prompt"].(string)
		if prompt == "" {
			return "", fmt.Errorf("missing prompt")
		}
		width := 512
		height := 512
		seed := 0
		if v, ok := args["width"]; ok {
			switch n := v.(type) {
			case float64:
				width = int(n)
			case int:
				width = n
			}
		}
		if v, ok := args["height"]; ok {
			switch n := v.(type) {
			case float64:
				height = int(n)
			case int:
				height = n
			}
		}
		if v, ok := args["seed"]; ok {
			switch n := v.(type) {
			case float64:
				seed = int(n)
			case int:
				seed = n
			}
		}

		// Check if image model is configured
		if strings.TrimSpace(r.imageModel) == "" {
			return "", fmt.Errorf("no image generation model configured - please set OLLAMA_MODEL_IMAGE in settings")
		}

		// Generate unique ID for this generation
		genID := fmt.Sprintf("gen_%d", time.Now().UnixNano())
		if r.imageProgressHandler != nil {
			r.imageProgressHandler.SetGenerationID(genID)
		}

		// Create generations directory if not exists
		genDir := filepath.Join(r.workspace, "generations")
		if err := os.MkdirAll(genDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create generations directory: %w", err)
		}

		// Generate unique filename
		timestamp := time.Now().Format("20060102_150405")
		filename := fmt.Sprintf("generated_%s_%dx%d.png", timestamp, width, height)
		filepath := filepath.Join(genDir, filename)

		// Call image generation via client
		req := ollama.GenerateRequest{
			Model:  r.imageModel,
			Prompt: prompt,
			Options: map[string]any{
				"width":  width,
				"height": height,
				"steps":  r.imageSteps,
				"seed":   seed,
			},
		}

		var imageData string
		steps := r.imageSteps
		if steps <= 0 {
			steps = 4
		}

		// Track progress message ID for editing
		var progressMsgID int

		err := r.client.GenerateStream(ctx, req, func(chunk ollama.GenerateResponse) error {
			if chunk.Total > 0 {
				log.Printf("[generate_image] Progress: %d/%d", chunk.Completed, chunk.Total)
				// Call progress handler if set (e.g., for Telegram/Web UI updates)
				if r.imageProgressHandler != nil {
					r.imageProgressHandler.OnProgress(chunk.Completed, chunk.Total, "generating")
				}
			}
			// Debug logging
			if chunk.Done {
				log.Printf("[generate_image] Chunk done: Response len=%d, Images len=%d", len(chunk.Response), len(chunk.Images))
				if chunk.Response != "" {
					log.Printf("[generate_image] Response starts with: %.50s...", chunk.Response)
				}
			}
			// Image data comes in Response field (base64 encoded PNG)
			if chunk.Done && chunk.Response != "" {
				imageData = chunk.Response
			}
			// Also check Images field as fallback
			if chunk.Done && imageData == "" && len(chunk.Images) > 0 && chunk.Images[0] != "" {
				imageData = chunk.Images[0]
				log.Printf("[generate_image] Got image from Images field instead")
			}
			_ = progressMsgID // may be used by handler
			return nil
		})

		if err != nil {
			return "", fmt.Errorf("image generation failed: %w", err)
		}

		if imageData == "" {
			return "", fmt.Errorf("no image data received from model")
		}

		// Decode base64 and save
		imageBytes, err := base64.StdEncoding.DecodeString(imageData)
		if err != nil {
			return "", fmt.Errorf("failed to decode image data: %w", err)
		}

		if err := os.WriteFile(filepath, imageBytes, 0644); err != nil {
			return "", fmt.Errorf("failed to save image: %w", err)
		}

		log.Printf("[generate_image] Image saved to: %s (%d bytes)", filepath, len(imageBytes))
		// Call completion handler if set
		if r.imageProgressHandler != nil {
			r.imageProgressHandler.OnComplete(filepath)
		}
		return fmt.Sprintf("Image generated successfully: %s", filepath), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
