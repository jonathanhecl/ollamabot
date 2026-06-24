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
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

// ApprovalHandler is implemented by clients wishing to approve or deny execution of risky tools.
type ApprovalHandler interface {
	RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error)
}

// ClarificationHandler is implemented by clients wishing to ask clarifying questions to the user.
type ClarificationHandler interface {
	RequestClarification(ctx context.Context, question string, options []string) (string, error)
}

// PlanConfirmationHandler is implemented by clients wishing to present a plan to the user for approval.
type PlanConfirmationHandler interface {
	RequestPlanApproval(ctx context.Context, summary string, steps []string) (bool, error)
}

// PlanProgressHandler is notified when a session plan advances.
type PlanProgressHandler func(sessionID string, plan sessions.SessionPlan)

type ApprovalProgressHandler interface {
	OnApprovalPending(tool string, args any, label string)
	OnApprovalResolved(tool string, approved bool, remembered bool)
}

// Registry holds available tool definitions and their handlers.
type Registry struct {
	enabled                 map[string]bool
	defs                    []ollama.Tool
	workspace               string
	memoryStore             *memory.Store
	client                  *ollama.Client
	embedModel              string
	todoStore               *TodoStore
	approvalHandler         ApprovalHandler
	approvalService         *sessions.ApprovalService
	clarificationHandler    ClarificationHandler
	planConfirmationHandler PlanConfirmationHandler
	planConfirmMode         string
	skillsPath              string
	searchCfg               SearchConfig
	imageModel              string
	imageSteps              int
	imageProgressHandler    ImageProgressHandler
	attachmentHandler       AttachmentGeneratedHandler
	sessionID               string
	sessionsPath            string
	sessionStore            *sessions.Store
	planProgressHandler     PlanProgressHandler
	approvalProgressHandler ApprovalProgressHandler
}

// ImageProgressHandler is called during image generation with progress updates
type ImageProgressHandler interface {
	OnProgress(genID string, completed, total int, message string, width, height int)
	OnComplete(genID string, imagePath string, width, height int)
	OnError(genID string, err error)
}

// AttachmentGeneratedHandler is called when a tool generates an attachment
// that should be registered on the current assistant message.
type AttachmentGeneratedHandler interface {
	OnAttachmentGenerated(sessionID string, ref string, mime string, path string)
}

// SetApprovalHandler assigns a callback handler to approve risky tools.
func (r *Registry) SetApprovalHandler(h ApprovalHandler) {
	r.approvalHandler = h
}

// SetApprovalService assigns the session-aware approval coordinator.
func (r *Registry) SetApprovalService(s *sessions.ApprovalService) {
	r.approvalService = s
}

// SetClarificationHandler assigns a callback handler to ask clarification questions.
func (r *Registry) SetClarificationHandler(h ClarificationHandler) {
	r.clarificationHandler = h
}

// SetPlanConfirmationHandler assigns a callback handler to present a plan.
func (r *Registry) SetPlanConfirmationHandler(h PlanConfirmationHandler) {
	r.planConfirmationHandler = h
}

// SetPlanConfirmMode sets the plan confirmation mode (always, auto, smart).
func (r *Registry) SetPlanConfirmMode(mode string) {
	r.planConfirmMode = mode
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

// SetAttachmentGeneratedHandler assigns a callback handler when a tool generates an attachment.
func (r *Registry) SetAttachmentGeneratedHandler(h AttachmentGeneratedHandler) {
	r.attachmentHandler = h
}

// SetSessionID sets the current session ID for organizing generated files.
func (r *Registry) SetSessionID(id string) {
	r.sessionID = id
}

// SetSessionsPath sets the path where session data (including attachments) is stored.
func (r *Registry) SetSessionsPath(path string) {
	r.sessionsPath = path
}

// SetSessionStore assigns the store used by session-aware tools.
func (r *Registry) SetSessionStore(store *sessions.Store) {
	r.sessionStore = store
}

// SetPlanProgressHandler assigns a callback fired when complete_plan_step advances.
func (r *Registry) SetPlanProgressHandler(h PlanProgressHandler) {
	r.planProgressHandler = h
}

func (r *Registry) SetApprovalProgressHandler(h ApprovalProgressHandler) {
	r.approvalProgressHandler = h
}

// ActiveSessionPlan returns the current session plan, if any.
func (r *Registry) ActiveSessionPlan() (*sessions.SessionPlan, error) {
	store := r.sessionStore
	if store == nil && strings.TrimSpace(r.sessionsPath) != "" {
		store = sessions.NewStore(r.sessionsPath)
	}
	if store == nil || strings.TrimSpace(r.sessionID) == "" {
		return nil, nil
	}
	sess, err := store.Get(r.sessionID)
	if err != nil {
		return nil, err
	}
	if sess.ActivePlan == nil {
		return nil, nil
	}
	plan := *sess.ActivePlan
	return &plan, nil
}

func parsePlanResumeAfter(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("resume_after is required")
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("resume_after duration must be positive")
		}
		return now.Add(d), nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		if !t.After(now) {
			return time.Time{}, fmt.Errorf("resume_after timestamp must be in the future")
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("resume_after must be a positive duration (e.g. 30m, 2h) or an RFC3339 timestamp")
}

// NewRegistry creates a registry with the given feature toggles.
func NewRegistry(webSearch bool, workspace string, memoryStore *memory.Store, client *ollama.Client, embedModel string, searchCfg SearchConfig) *Registry {
	r := &Registry{
		enabled:         map[string]bool{},
		workspace:       workspace,
		memoryStore:     memoryStore,
		client:          client,
		embedModel:      embedModel,
		todoStore:       NewTodoStore(),
		planConfirmMode: "smart",
		skillsPath:      "skills",
		searchCfg:       searchCfg,
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
			Description: "Run a shell command (e.g. ffmpeg, ffprobe, pandoc, python3) inside the workspace directory. Use this to process binary files, convert formats, etc. Always requires user approval before execution.",
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
			Description: "Ask the user ONE clarifying question, then provide clickable affirmative option statements (at least 2). The question goes in 'question'; each option must be a statement the user selects, never another question.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The single clarifying question to ask the user.",
					},
					"options": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":        "string",
							"description": "An affirmative statement the user can click to choose. Must NOT be a question. Good: \"Start a complex plan\", \"Respond with a cheerful tone\". Bad: \"Do you want a plan?\", \"¿Quieres iniciar un plan?\".",
						},
						"description": "At least 2 affirmative option statements (not questions). Write them as first-person or imperative choices the user can pick.",
					},
				},
				"required": []string{"question", "options"},
			},
		},
	})

	r.enabled["present_plan"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "present_plan",
			Description: "Present a structured top-level plan to the user for approval before executing a complex multi-step task. After approval, call complete_plan_step exactly once only when each top-level step is fully finished.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "A brief summary explaining what the agent plans to accomplish.",
					},
					"steps": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "The ordered list of concrete, actionable steps the agent will perform.",
					},
				},
				"required": []string{"summary", "steps"},
			},
		},
	})

	r.enabled["complete_plan_step"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "complete_plan_step",
			Description: "Mark exactly one top-level approved plan step as completed. Call this only after finishing all sub-work for the current plan step and before moving to the next step.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"note": map[string]any{
						"type":        "string",
						"description": "Optional short note describing what was completed for the current top-level plan step.",
					},
				},
			},
		},
	})

	r.enabled["defer_plan_continuation"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "defer_plan_continuation",
			Description: "Pause an approved active plan without abandoning it. Use only when the plan cannot be continued in the current turn; you must explain the reason, when to resume, and what remains for the user.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":        "string",
						"description": "Why the plan cannot continue right now.",
					},
					"resume_after": map[string]any{
						"type":        "string",
						"description": "When to resume. Accepts a Go duration like '30m', '2h', or an RFC3339 timestamp.",
					},
					"follow_up_summary": map[string]any{
						"type":        "string",
						"description": "Short summary of the remaining work to continue later.",
					},
					"user_message": map[string]any{
						"type":        "string",
						"description": "Clear message to show the user explaining the pause and follow-up.",
					},
				},
				"required": []string{"reason", "resume_after", "follow_up_summary", "user_message"},
			},
		},
	})

	// Register Generate Image Tool (enabled when image model is configured)
	r.enabled["generate_image"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "generate_image",
			Description: "Generate an image using a diffusion model (Flux, etc.). Use this when the user explicitly or implicitly requests image generation (e.g., 'generate an image of...', 'create a picture of...', 'draw...', 'imagine...'). The prompt MUST be in English for best quality and effectiveness (translate user prompt to English if necessary). The agent should choose appropriate resolution: 512x512 for square/portrait, 1024x512 for landscape, 512x1024 for tall/portrait. Returns the attachment reference of the generated image.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "Detailed description of the image to generate. MUST be in English for the diffusion model to produce optimal results (translate to English if the user input is in another language). Be specific about subject, style, lighting, mood, colors.",
					},
					"width": map[string]any{
						"type":        "integer",
						"description": "Image width in pixels. Standard options: 512 (default), 768, 1024. Custom resolutions (e.g., 64, 128, 256) are also supported for logos, icons, and buttons.",
						"default":     512,
					},
					"height": map[string]any{
						"type":        "integer",
						"description": "Image height in pixels. Standard options: 512 (default), 768, 1024. Custom resolutions (e.g., 64, 128, 256) are also supported for logos, icons, and buttons.",
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

	// Register session attachment browsing tools
	r.enabled["list_session_attachments"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "list_session_attachments",
			Description: "List all attachments stored in the current session, including images, audio, and files. Returns names, kinds, sizes, and references. Use this when the user asks about previously uploaded or generated media.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	})

	r.enabled["view_session_attachment"] = true
	r.defs = append(r.defs, ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "view_session_attachment",
			Description: "View the contents of a session attachment by its reference name. For images, returns the base64-encoded image data that can be sent to a vision model. For other files, returns the raw content or a description.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ref": map[string]any{
						"type":        "string",
						"description": "The attachment reference name (e.g. 'generated_20260102_150405_512x512.png' or '0_0.json').",
					},
				},
				"required": []string{"ref"},
			},
		},
	})

	return r
}

// Definitions returns the Ollama tool definitions to expose to the model.
func (r *Registry) Definitions() []ollama.Tool {
	if r.planConfirmMode == "auto" {
		var filtered []ollama.Tool
		for _, d := range r.defs {
			if d.Function.Name != "present_plan" {
				filtered = append(filtered, d)
			}
		}
		return filtered
	}
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

	// Risk check: write/edit outside the workspace and shell commands require approval.
	if (r.approvalService != nil || r.approvalHandler != nil) && (name == "Write" || name == "Edit" || name == "execute_command") {
		filePath, _ := args["file_path"].(string)
		isSafe := false
		if name != "execute_command" && filePath != "" {
			_, err := ResolveAndValidatePath(r.workspace, filePath)
			isSafe = err == nil
		}

		// execute_command always requires approval regardless of path
		if name == "execute_command" {
			isSafe = false
		}

		if !isSafe {
			if r.approvalService != nil && strings.TrimSpace(r.sessionID) != "" {
				if r.approvalService.HasGrant(r.sessionID, name, args) {
					log.Printf("[tool] Risky tool %q is approved for this session. Bypassing approval.", name)
				} else {
					log.Printf("[tool] Intercepting risky tool %q. Requesting session approval...", name)
					_, label := sessions.FormatApprovalSignature(name, args, r.workspace)
					if r.approvalProgressHandler != nil {
						r.approvalProgressHandler.OnApprovalPending(name, args, label)
					}
					approved, err := r.approvalService.RequestApproval(ctx, r.sessionID, name, args)
					remembered := approved && r.approvalService.HasGrant(r.sessionID, name, args)
					if r.approvalProgressHandler != nil {
						r.approvalProgressHandler.OnApprovalResolved(name, approved, remembered)
					}
					if err != nil {
						log.Printf("[tool] Approval error for %q: %v", name, err)
						return "", fmt.Errorf("tool approval failed: %w", err)
					}
					if !approved {
						log.Printf("[tool] Risky tool %q execution DENIED by user", name)
						return "Error: Execution denied by user.", nil
					}
					log.Printf("[tool] Risky tool %q execution APPROVED by user", name)
				}
			} else if r.approvalHandler != nil {
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
			}
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
		if err := validateClarificationOptions(options); err != nil {
			return "", err
		}
		if r.clarificationHandler == nil {
			return "", fmt.Errorf("no clarification handler configured")
		}
		return r.clarificationHandler.RequestClarification(ctx, question, options)
	case "present_plan":
		summary, _ := args["summary"].(string)
		stepsVal := args["steps"]
		if summary == "" || stepsVal == nil {
			return "", fmt.Errorf("missing required arguments for present_plan")
		}
		var steps []string
		bytes, err := json.Marshal(stepsVal)
		if err != nil {
			return "", fmt.Errorf("failed to encode steps: %w", err)
		}
		if err := json.Unmarshal(bytes, &steps); err != nil {
			return "", fmt.Errorf("failed to decode steps: %w", err)
		}
		steps = sessions.NormalizePlanSteps(steps)
		if len(steps) == 0 {
			return "", fmt.Errorf("present_plan requires at least 1 step")
		}
		if activePlan, planErr := r.ActiveSessionPlan(); planErr == nil && activePlan != nil && activePlan.Status == sessions.PlanStatusActive {
			current := activePlan.Completed + 1
			if current > len(activePlan.Steps) {
				current = len(activePlan.Steps)
			}
			stepText := activePlan.Steps[activePlan.Completed]
			return fmt.Sprintf("Plan already approved (step %d of %d: %s). Proceed with execution; do not call present_plan again.", current, len(activePlan.Steps), stepText), nil
		}
		if r.planConfirmationHandler == nil {
			store := r.sessionStore
			if store == nil && strings.TrimSpace(r.sessionsPath) != "" {
				store = sessions.NewStore(r.sessionsPath)
			}
			if store != nil && strings.TrimSpace(r.sessionID) != "" {
				plan, err := sessions.ActivatePlan(store, r.sessionID, summary, steps)
				if err != nil {
					return "", err
				}
				if r.planProgressHandler != nil {
					r.planProgressHandler(r.sessionID, plan)
				}
			}
			return "Plan auto-approved and activated. Proceeding with execution.", nil
		}
		approved, err := r.planConfirmationHandler.RequestPlanApproval(ctx, summary, steps)
		if err != nil {
			return "", fmt.Errorf("plan approval failed: %w", err)
		}
		if !approved {
			return "Plan rejected by the user. Please stop and ask the user for clarification or propose a new plan.", nil
		}
		if activePlan, planErr := r.ActiveSessionPlan(); planErr == nil && activePlan != nil && r.planProgressHandler != nil {
			r.planProgressHandler(r.sessionID, *activePlan)
		}
		return "Plan approved by the user. Proceed with the steps.", nil
	case "complete_plan_step":
		note, _ := args["note"].(string)
		store := r.sessionStore
		if store == nil && strings.TrimSpace(r.sessionsPath) != "" {
			store = sessions.NewStore(r.sessionsPath)
		}
		if store == nil {
			return "", fmt.Errorf("session store is required to complete a plan step")
		}
		if strings.TrimSpace(r.sessionID) == "" {
			return "", fmt.Errorf("session ID is required to complete a plan step")
		}
		plan, message, err := sessions.CompletePlanStep(store, r.sessionID, note)
		if err != nil {
			return "", err
		}
		if r.planProgressHandler != nil {
			r.planProgressHandler(r.sessionID, plan)
		}
		return message, nil
	case "defer_plan_continuation":
		reason, _ := args["reason"].(string)
		resumeAfter, _ := args["resume_after"].(string)
		followUpSummary, _ := args["follow_up_summary"].(string)
		userMessage, _ := args["user_message"].(string)
		if strings.TrimSpace(userMessage) == "" {
			return "", fmt.Errorf("user_message is required")
		}
		resumeAt, err := parsePlanResumeAfter(resumeAfter, time.Now())
		if err != nil {
			return "", err
		}
		store := r.sessionStore
		if store == nil && strings.TrimSpace(r.sessionsPath) != "" {
			store = sessions.NewStore(r.sessionsPath)
		}
		if store == nil {
			return "", fmt.Errorf("session store is required to defer a plan")
		}
		if strings.TrimSpace(r.sessionID) == "" {
			return "", fmt.Errorf("session ID is required to defer a plan")
		}
		plan, message, err := sessions.DeferPlanContinuation(store, r.sessionID, reason, resumeAt, followUpSummary)
		if err != nil {
			return "", err
		}
		if r.planProgressHandler != nil {
			r.planProgressHandler(r.sessionID, plan)
		}
		return userMessage + "\n\n" + message, nil
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

		// Create session-specific attachments directory for generated images
		var attDir string
		if strings.TrimSpace(r.sessionID) != "" {
			if strings.TrimSpace(r.sessionsPath) != "" {
				attDir = filepath.Join(r.sessionsPath, r.sessionID, "attachments")
			} else {
				attDir = filepath.Join(r.workspace, "sessions", r.sessionID, "attachments")
			}
		} else {
			attDir = filepath.Join(r.workspace, "generations")
		}
		if err := os.MkdirAll(attDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create attachments directory: %w", err)
		}

		// Generate unique filename
		timestamp := time.Now().Format("20060102_150405")
		filename := fmt.Sprintf("generated_%s_%dx%d.png", timestamp, width, height)
		filePath := filepath.Join(attDir, filename)

		// Call image generation via client
		req := ollama.GenerateRequest{
			Model:  r.imageModel,
			Prompt: prompt,
			Width:  width,
			Height: height,
			Steps:  r.imageSteps,
			Options: map[string]any{
				"seed": seed,
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
					r.imageProgressHandler.OnProgress(genID, chunk.Completed, chunk.Total, "generating", width, height)
				}
			}
			// Image data comes in Image field on the final done chunk
			if chunk.Done && chunk.Image != "" {
				imageData = chunk.Image
				log.Printf("[generate_image] Got image data: %d bytes", len(imageData))
			}
			_ = progressMsgID // may be used by handler
			return nil
		})

		if err != nil {
			if r.imageProgressHandler != nil {
				r.imageProgressHandler.OnError(genID, err)
			}
			return "", fmt.Errorf("image generation failed: %w", err)
		}

		if imageData == "" {
			noImageErr := fmt.Errorf("no image data received from model")
			if r.imageProgressHandler != nil {
				r.imageProgressHandler.OnError(genID, noImageErr)
			}
			return "", noImageErr
		}

		// Decode base64 and save
		imageBytes, err := base64.StdEncoding.DecodeString(imageData)
		if err != nil {
			return "", fmt.Errorf("failed to decode image data: %w", err)
		}

		if err := os.WriteFile(filePath, imageBytes, 0644); err != nil {
			return "", fmt.Errorf("failed to save image: %w", err)
		}

		log.Printf("[generate_image] Image saved to: %s (%d bytes)", filePath, len(imageBytes))

		// Notify attachment handler so the recorder can register it on the assistant message
		if r.attachmentHandler != nil && strings.TrimSpace(r.sessionID) != "" {
			r.attachmentHandler.OnAttachmentGenerated(r.sessionID, filename, "image/png", filePath)
		}

		// Call completion handler if set - pass real filesystem path
		if r.imageProgressHandler != nil {
			r.imageProgressHandler.OnComplete(genID, filePath, width, height)
		}
		return fmt.Sprintf("Image generated and saved as session attachment. Reference: %s, Path: %s, Size: %dx%d", filename, filePath, width, height), nil
	case "list_session_attachments":
		if strings.TrimSpace(r.sessionID) == "" {
			return "", fmt.Errorf("no active session")
		}
		var attDir string
		if strings.TrimSpace(r.sessionsPath) != "" {
			attDir = filepath.Join(r.sessionsPath, r.sessionID, "attachments")
		} else {
			attDir = filepath.Join(r.workspace, "sessions", r.sessionID, "attachments")
		}
		entries, err := os.ReadDir(attDir)
		if err != nil {
			return "No attachments found in this session.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Session attachments (%d items):\n", len(entries)))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			kind := "file"
			mime := "application/octet-stream"
			name := e.Name()
			if strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".gif") || strings.HasSuffix(name, ".webp") {
				kind = "image"
				mime = "image/png"
				if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") {
					mime = "image/jpeg"
				}
			} else if strings.HasSuffix(name, ".wav") || strings.HasSuffix(name, ".mp3") || strings.HasSuffix(name, ".ogg") {
				kind = "audio"
				mime = "audio/wav"
			} else if strings.HasSuffix(name, ".json") {
				// Legacy attachmentStorage format: read to get actual kind
				data, err := os.ReadFile(filepath.Join(attDir, name))
				if err == nil {
					var storage struct {
						Name string `json:"name"`
						Mime string `json:"mime"`
						Kind string `json:"kind"`
					}
					if json.Unmarshal(data, &storage) == nil {
						if storage.Kind != "" {
							kind = storage.Kind
						}
						if storage.Mime != "" {
							mime = storage.Mime
						}
						if storage.Name != "" {
							name = storage.Name
						}
					}
				}
			}
			sb.WriteString(fmt.Sprintf("- ref: %s | name: %s | kind: %s | mime: %s | size: %d bytes\n", e.Name(), name, kind, mime, info.Size()))
		}
		return sb.String(), nil
	case "view_session_attachment":
		ref, _ := args["ref"].(string)
		if ref == "" {
			return "", fmt.Errorf("missing ref")
		}
		if strings.TrimSpace(r.sessionID) == "" {
			return "", fmt.Errorf("no active session")
		}
		var attDir string
		if strings.TrimSpace(r.sessionsPath) != "" {
			attDir = filepath.Join(r.sessionsPath, r.sessionID, "attachments")
		} else {
			attDir = filepath.Join(r.workspace, "sessions", r.sessionID, "attachments")
		}
		path := filepath.Join(attDir, ref)
		// Prevent directory traversal
		if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(attDir)) {
			return "", fmt.Errorf("invalid ref")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read attachment: %w", err)
		}
		// Try to parse as JSON attachmentStorage first
		var storage struct {
			Name        string `json:"name"`
			Mime        string `json:"mime"`
			Kind        string `json:"kind"`
			Data        string `json:"data"`
			Description string `json:"description,omitempty"`
		}
		if err := json.Unmarshal(data, &storage); err == nil && storage.Data != "" {
			if strings.HasPrefix(storage.Mime, "image/") {
				return fmt.Sprintf("Attachment: %s (kind=%s, mime=%s)\n[base64_image_data]: %s", storage.Name, storage.Kind, storage.Mime, storage.Data), nil
			}
			return fmt.Sprintf("Attachment: %s (kind=%s, mime=%s)\nContent: %s", storage.Name, storage.Kind, storage.Mime, storage.Data), nil
		}
		// Binary file (e.g. generated PNG)
		mime := "application/octet-stream"
		if strings.HasSuffix(ref, ".png") {
			mime = "image/png"
		} else if strings.HasSuffix(ref, ".jpg") || strings.HasSuffix(ref, ".jpeg") {
			mime = "image/jpeg"
		} else if strings.HasSuffix(ref, ".gif") {
			mime = "image/gif"
		} else if strings.HasSuffix(ref, ".webp") {
			mime = "image/webp"
		} else if strings.HasSuffix(ref, ".wav") {
			mime = "audio/wav"
		} else if strings.HasSuffix(ref, ".mp3") {
			mime = "audio/mpeg"
		}
		if strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "audio/") {
			b64 := base64.StdEncoding.EncodeToString(data)
			return fmt.Sprintf("Attachment: %s (mime=%s, size=%d bytes)\n[base64_data]: %s", ref, mime, len(data), b64), nil
		}
		// Text fallback
		return fmt.Sprintf("Attachment: %s (mime=%s, size=%d bytes)\nContent:\n%s", ref, mime, len(data), string(data)), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
