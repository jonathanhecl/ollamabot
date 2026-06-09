package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/skills"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

const (
	MaxIterations = 50
)

type StreamHandler interface {
	OnThinking(delta string)
	OnContent(delta string)
	OnToolCall(call ollama.ToolCall)
	OnToolStart(name string, args any)
	OnToolResult(name string, result string)
	OnMediaPreProcessing(content string)
	OnDone(resp ollama.ChatResponse)
}

type Agent struct {
	cfg         config.Config
	client      *ollama.Client
	registry    *tools.Registry
	paths       *pathMemory
	currentGoal string
	mu          sync.RWMutex
}

func NewAgent(cfg config.Config, client *ollama.Client, registry *tools.Registry) *Agent {
	return &Agent{
		cfg:      cfg,
		client:   client,
		registry: registry,
		paths:    newPathMemory(cfg.Workspace),
	}
}

// Run executes the iterative multi-turn planning and tool loop.
func (a *Agent) Run(ctx context.Context, model string, messages []ollama.Message, think bool, handler StreamHandler) ([]ollama.Message, error) {
	toolCallCounts := make(map[string]int)
	// Find the current goal from the last user message
	var goal string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			goal = messages[i].Content
			break
		}
	}
	a.mu.Lock()
	a.currentGoal = goal
	a.mu.Unlock()

	// Proactive Update Soul from User prompt
	if goal != "" {
		_ = UpdateSoulFromPrompt(goal)
	}

	// Proactive Auto-RAG context pre-fetching
	var recalledMemoriesBlock string
	if a.registry != nil && a.registry.MemoryStore() != nil && a.cfg.OllamaModelEmbed != "" && goal != "" {
		embedResp, err := a.client.Embed(ctx, ollama.EmbedRequest{
			Model: a.cfg.OllamaModelEmbed,
			Input: goal,
		})
		if err == nil && len(embedResp.Embeddings) > 0 {
			results := a.registry.MemoryStore().Search(embedResp.Embeddings[0], 3)
			if len(results) > 0 {
				var sb strings.Builder
				hasMatchingMemories := false
				for _, res := range results {
					if res.Score >= 0.70 {
						if !hasMatchingMemories {
							sb.WriteString("# Recalled Context (Long-term Memory)\n")
							sb.WriteString("The following relevant information was retrieved from your long-term memory:\n")
							hasMatchingMemories = true
						}
						fmt.Fprintf(&sb, "- %s (Source: %s, Relevance: %.2f)\n", res.Text, res.Source, res.Score)
					}
				}
				if hasMatchingMemories {
					recalledMemoriesBlock = sb.String()
				}
			}
		} else if err != nil {
			log.Printf("[Agent Run] Memory pre-fetch embedding error: %v (gracefully continuing)", err)
		}
	}

	// Load and inject custom skills from configurable skills path
	skillsDir := a.cfg.SkillsPath
	var skillsBlock string
	if cat, err := skills.NewCatalog(skillsDir); err == nil {
		if loaded, err := cat.LoadAll(); err == nil && len(loaded) > 0 {
			skillsBlock = skills.RenderBlock(loaded)
		}
	}

	emptyChatErrRetries := 0

	for i := 0; i < MaxIterations; i++ {
		var systemPrefix []ollama.Message

		// Load SOUL.md dynamically
		if soul, err := LoadSoul(); err == nil && soul != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: soul,
			})
		}

		// Load USER_PROFILE.md dynamically
		if profile, err := LoadUserProfile(); err == nil && profile != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: "# User Profile & Preferences\n" + profile,
			})
		}

		// Inject memory tools instruction
		if a.cfg.OllamaModelEmbed != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: "You have access to long-term memory tools (memory_add, memory_search, memory_delete, memory_list). Manage your own memory proactively:\n- Store important facts, user preferences, decisions, and context using memory_add.\n- Search memory when the question may benefit from past knowledge using memory_search. Always search memory first before adding new memories.\n- Delete outdated or incorrect information using memory_delete.\n- Review stored memories with memory_list before deciding what to add, update, or remove.\n- Consolidate & Deduplicate: To prevent duplicate or obsolete memories, ALWAYS search for related facts first. If you learn updated information about an existing memory, you must DELETE the old version (using memory_delete with its ID) BEFORE adding the new version. Do not store near-identical or overlapping facts.\n- Prioritize: only store information that is likely to be useful later.",
			})
		}

		// Inject proactive recalled memories if any
		if recalledMemoriesBlock != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: recalledMemoriesBlock,
			})
		}

		// Inject loaded skills block if discovered
		if skillsBlock != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: "# Loaded Custom Skills\n\n" + skillsBlock,
			})
		}

		// 1. Check Todo list status
		todoStore := a.registry.TodoStore()
		var todoNote string
		hasPending := false
		if todoStore != nil {
			snap := todoStore.Snapshot()
			if len(snap) > 0 {
				todoNote = buildTodoProgressNote(snap)
				for _, it := range snap {
					if it.Status == tools.TodoStatusPending || it.Status == tools.TodoStatusInProgress {
						hasPending = true
					}
				}
			}
		}

		if todoNote != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: todoNote,
			})
		}

		// Inject the current goal reinforcement
		if goal != "" {
			goalReinforce := fmt.Sprintf("Your current user goal is:\n<<<USER_GOAL>>>\n%s\n<<<END_USER_GOAL>>>\nKeep executing until all steps are done.", goal)
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: goalReinforce,
			})
		}

		// Inject reinforcement for clarification
		clarificationReinforce := "If the user's instructions are ambiguous, incomplete, or you need more details to plan or execute safely, you MUST use the 'ask_clarification' tool to present a clear question with at least 2 proposed options. Do not assume or guess if key details are missing."
		systemPrefix = append(systemPrefix, ollama.Message{
			Role:    "system",
			Content: clarificationReinforce,
		})

		// 2. Build system instructions incorporating Todo checklists, goals, and skills
		activeMessages := make([]ollama.Message, 0, len(systemPrefix)+len(messages))
		activeMessages = append(activeMessages, systemPrefix...)
		activeMessages = append(activeMessages, messages...)

		// 3. Prepare the request
		req := ollama.ChatRequest{
			Model:    model,
			Messages: activeMessages,
			Think:    think,
		}
		defs := a.registry.Definitions()
		if len(defs) > 0 {
			req.Tools = defs
		}

		// 4. Stream response turn
		var assistantContent strings.Builder
		var assistantThinking strings.Builder
		var toolCalls []ollama.ToolCall
		seenTool := map[string]struct{}{}
		done := false
		var contentFilter StreamThinkingFilter

		err := a.client.ChatStream(ctx, req, func(chunk ollama.ChatResponse) error {
			if chunk.Message.Thinking != "" {
				assistantThinking.WriteString(chunk.Message.Thinking)
				if handler != nil {
					handler.OnThinking(chunk.Message.Thinking)
				}
			}
			if chunk.Message.Content != "" {
				// Keep raw content for XML tool fallback parsing, but stream a
				// version with residual thinking tokens (<think>, <thought>, ...) removed.
				assistantContent.WriteString(chunk.Message.Content)
				if handler != nil {
					if emit := contentFilter.Write(chunk.Message.Content); emit != "" {
						handler.OnContent(emit)
					}
				}
			}
			for _, call := range chunk.Message.ToolCalls {
				key := call.Function.Name + "|" + string(call.Function.Arguments)
				if _, ok := seenTool[key]; ok {
					continue
				}
				seenTool[key] = struct{}{}
				toolCalls = append(toolCalls, call)
				if handler != nil {
					handler.OnToolCall(call)
				}
			}
			if chunk.Done {
				done = true
				if handler != nil {
					handler.OnDone(chunk)
				}
			}
			return nil
		})
		if err != nil {
			return messages, err
		}
		if !done {
			return messages, fmt.Errorf("Ollama connection closed unexpectedly")
		}

		// Emit any content held back by the thinking-token filter.
		if handler != nil {
			if emit := contentFilter.Flush(); emit != "" {
				handler.OnContent(emit)
			}
		}

		assistantText := assistantContent.String()

		// 5. XML Fallback Parsing: recover tools if native tool calling failed but XML tag exists
		if len(toolCalls) == 0 {
			if fallbackName, fallbackParams, ok := parseXMLFallback(assistantText); ok {
				argsJSON, _ := json.Marshal(fallbackParams)
				toolCalls = append(toolCalls, ollama.ToolCall{
					Type: "function",
					Function: ollama.ToolFunction{
						Name:      fallbackName,
						Arguments: argsJSON,
					},
				})
			} else if errMsg, malformed := detectMalformedXMLFallback(assistantText); malformed {
				messages = append(messages, ollama.Message{
					Role:    "system",
					Content: errMsg,
				})
				if handler != nil {
					handler.OnContent("\n\n" + errMsg)
				}
				continue
			}
		}

		// 6. Append assistant message to local trace history.
		// Strip residual thinking tokens (<think>, <thought>, ...) from the stored
		// content so downstream consumers (Telegram messages, persisted sessions)
		// receive clean final text.
		cleanedText := CleanThinkingTokens(assistantText)
		assistantMsg := ollama.Message{
			Role:      "assistant",
			Content:   cleanedText,
			Thinking:  assistantThinking.String(),
			ToolCalls: toolCalls,
		}
		messages = append(messages, assistantMsg)

		// 7. Execute tool calls if any
		if len(toolCalls) > 0 {
			emptyChatErrRetries = 0

			for _, call := range toolCalls {
				toolName := call.Function.Name
				var params map[string]any
				_ = json.Unmarshal(call.Function.Arguments, &params)
				if params == nil {
					params = map[string]any{}
				}

				// Path parameter rescue
				a.rescuePathParam(toolName, params)

				// Re-serialize params
				rescuedArgsJSON, _ := json.Marshal(params)
				call.Function.Arguments = rescuedArgsJSON

				if handler != nil {
					handler.OnToolStart(toolName, params)
				}

				// Execute tool
				result, terr := a.registry.Execute(ctx, call)
				if terr != nil {
					result = fmt.Sprintf("Error: %v", terr)
				}

				// Check for repetitive loops
				argsStr := string(call.Function.Arguments)
				key := toolName + ":" + argsStr
				toolCallCounts[key]++
				if toolCallCounts[key] >= 3 {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: You have called tool '%s' with the identical arguments %d times. To avoid a repetitive loop, please check the file path, verify the contents of the file using read_file, or try a different approach.]", result, toolName, toolCallCounts[key])
				}

				// Remember observed paths
				isError := terr != nil || strings.HasPrefix(result, "Error")
				a.paths.RememberToolResult(toolName, params, result, isError)

				if handler != nil {
					handler.OnToolResult(toolName, result)
				}

				messages = append(messages, ollama.Message{
					Role:    "tool",
					Name:    toolName,
					Content: result,
				})
			}

			// Continue with next loop turn so the LLM processes results
			continue
		}

		// 8. Handle empty completions (including content that was purely residual
		// thinking tokens and is now empty after cleaning).
		if strings.TrimSpace(cleanedText) == "" && (strings.TrimSpace(assistantThinking.String()) != "" || strings.TrimSpace(assistantText) != "") {
			if emptyChatErrRetries < 2 {
				emptyChatErrRetries++
				messages = append(messages, ollama.Message{
					Role:    "system",
					Content: "Previous attempt returned only thinking. Please produce a visible text response or call a tool.",
				})
				continue
			}
		}

		// 9. Enforce Todo Completion: refuse to end loop if Todos are pending
		if hasPending {
			messages = append(messages, ollama.Message{
				Role:    "system",
				Content: "There are still pending TODO items. Continue executing the remaining steps with tool calls — do not finish the turn with plain text.",
			})
			continue
		}

		// No more tools and no pending Todos: complete task cleanly!
		break
	}

	return messages, nil
}

func (a *Agent) rescuePathParam(toolName string, params map[string]any) {
	key := pathParamKeyForTool(toolName)
	if key == "" {
		return
	}
	raw, ok := params[key].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	if abs, rescued, ok := a.paths.Resolve(raw); ok {
		if abs != raw {
			params[key] = abs
			if rescued {
				log.Printf("[path memory] Rescued param %q -> %s", raw, abs)
			}
		}
	}
}

func buildTodoProgressNote(snap []tools.TodoItem) string {
	var b strings.Builder
	b.WriteString("TODO progress:\n")
	for _, it := range snap {
		status := it.Status
		if status == "" {
			status = tools.TodoStatusPending
		}
		switch status {
		case tools.TodoStatusCompleted:
			fmt.Fprintf(&b, "  [DONE] %s: %s\n", it.ID, it.Content)
		case tools.TodoStatusInProgress:
			fmt.Fprintf(&b, "  [IN PROGRESS] %s: %s — execute this step now and mark completed when done.\n", it.ID, it.Content)
		default:
			fmt.Fprintf(&b, "  [PENDING] %s: %s\n", it.ID, it.Content)
		}
	}
	b.WriteString("Use data from earlier tool results to complete pending steps. Do not repeat what is already done.")
	return b.String()
}
