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

		// 2. Build system instructions incorporating Todo checklists, goals, and skills
		activeMessages := make([]ollama.Message, len(messages))
		copy(activeMessages, messages)
		if todoNote != "" {
			activeMessages = append([]ollama.Message{{
				Role:    "system",
				Content: todoNote,
			}}, activeMessages...)
		}

		// Inject loaded skills block if discovered
		if skillsBlock != "" {
			activeMessages = append([]ollama.Message{{
				Role:    "system",
				Content: "# Loaded Custom Skills\n\n" + skillsBlock,
			}}, activeMessages...)
		}

		// Inject the current goal reinforcement
		if goal != "" {
			goalReinforce := fmt.Sprintf("Your current user goal is:\n<<<USER_GOAL>>>\n%s\n<<<END_USER_GOAL>>>\nKeep executing until all steps are done.", goal)
			activeMessages = append([]ollama.Message{{
				Role:    "system",
				Content: goalReinforce,
			}}, activeMessages...)
		}

		// Inject reinforcement for clarification
		clarificationReinforce := "If the user's instructions are ambiguous, incomplete, or you need more details to plan or execute safely, you MUST use the 'ask_clarification' tool to present a clear question with at least 2 proposed options. Do not assume or guess if key details are missing."
		activeMessages = append([]ollama.Message{{
			Role:    "system",
			Content: clarificationReinforce,
		}}, activeMessages...)

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
