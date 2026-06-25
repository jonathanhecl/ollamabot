package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
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
	OnContextOptimizationStart(tokensBefore int, percentBefore float64)
	OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64)
	OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int)
}

type Agent struct {
	cfgMgr      *config.Manager
	client      *ollama.Client
	registry    *tools.Registry
	paths       *pathMemory
	currentGoal string
	options     map[string]any
	mu          sync.RWMutex
}

func (a *Agent) config() config.Config {
	return a.cfgMgr.Get()
}

func NewAgent(cfg *config.Manager, client *ollama.Client, registry *tools.Registry) *Agent {
	// Configure image generation model in registry if available
	if registry != nil {
		registry.SetImageModel(cfg.Get().OllamaModelImage)
		registry.SetImageSteps(cfg.Get().OllamaImageSteps)
		registry.SetPlanConfirmMode(cfg.Get().PlanConfirmation)
	}
	return &Agent{
		cfgMgr:   cfg,
		client:   client,
		registry: registry,
		paths:    newPathMemory(cfg.Get().Workspace),
	}
}

func (a *Agent) SetOptions(opts map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.options = opts
}

// Run executes the iterative multi-turn planning and tool loop.
func (a *Agent) Run(ctx context.Context, model string, messages []ollama.Message, think bool, handler StreamHandler) ([]ollama.Message, error) {
	toolCallCounts := make(map[string]int)

	limit := a.getContextLimit(ctx, model)
	numCtx := 8192
	if limit > 32768 {
		numCtx = 32768
	} else if limit < 2048 {
		numCtx = 2048
	} else {
		numCtx = int(limit)
	}
	numPredict := 4096

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
	if a.registry != nil && a.registry.MemoryStore() != nil && a.config().OllamaModelEmbed != "" && goal != "" {
		embedResp, err := a.client.Embed(ctx, ollama.EmbedRequest{
			Model: a.config().OllamaModelEmbed,
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
	skillsDir := a.config().SkillsPath
	var skillsBlock string
	if cat, err := skills.NewCatalog(skillsDir); err == nil {
		if loaded, err := cat.LoadAll(); err == nil && len(loaded) > 0 {
			skillsBlock = skills.RenderBlock(loaded)
		}
	}

	emptyChatErrRetries := 0
	planStepHasAction := false
	planTextOnlyRetries := 0
	completedCleanly := false

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
		if a.config().OllamaModelEmbed != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: "You have access to long-term memory tools (memory_add, memory_search, memory_delete, memory_list). Manage your own memory proactively:\n- Store important facts, user preferences, decisions, and context using memory_add. Write self-contained, descriptive, and reusable memory entries (containing error patterns, technologies involved, file context, and the exact working solution) so they can be retrieved and applied effectively in both the current project and other contexts.\n- Search memory when the question may benefit from past knowledge using memory_search. Always search memory first before adding new memories.\n- Delete outdated or incorrect information using memory_delete.\n- Review stored memories with memory_list before deciding what to add, update, or remove.\n- Consolidate & Deduplicate: To prevent duplicate or obsolete memories, ALWAYS search for related facts first. If you learn updated information about an existing memory, you must DELETE the old version (using memory_delete with its ID) BEFORE adding the new version. Do not store near-identical or overlapping facts.\n- Prioritize: only store information that is likely to be useful later.\n- Lessons Learned: When you solve a difficult error, bug, or discover workspace-specific setups (e.g. missing dependencies, required environment variables, unique build scripts), store a concise 'lesson learned' memory with context using memory_add so you do not repeat the mistake in future tasks.",
			})
		}

		// Inject image generation capability instruction
		if strings.TrimSpace(a.config().OllamaModelImage) != "" {
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: "You have access to image generation via the `generate_image` tool. When the user requests image creation (e.g., 'generate an image of...', 'create a picture of...', 'draw...', 'imagine...'), use this tool. Choose appropriate resolution based on context: 512x512 for standard square images, 1024x512 for landscape, 512x1024 for portrait. You can also specify custom smaller or aspect-ratio dimensions (like 64, 128, 256, etc.) directly when generating specific UI assets like icons, buttons, or logos. Important: The prompt passed to the generate_image tool must be in English for the best results, so you must translate the user's prompt to detailed, descriptive English if it is in another language. Do NOT output the generated image filename, path, or reference (e.g. do not say 'Reference: generated_...' or 'Referencia: ...') in your response to the user, as the user interface automatically renders the generated image bubble under your message. Simply confirm that the image is ready.",
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
			goalReinforce := fmt.Sprintf("Your current user goal is:\n<<<USER_GOAL>>>\n%s\n<<<END_USER_GOAL>>>\nKeep executing until all steps are done. If an approved plan is active, do not stop until the plan is completed or you explicitly defer it with defer_plan_continuation and notify the user.", goal)
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: goalReinforce,
			})
		}

		// Inject reinforcement for clarification
		clarificationReinforce := "If the user's instructions are ambiguous, incomplete, or you need more details to plan or execute safely, you MUST use the 'ask_clarification' tool. Put the question only in 'question'. Every entry in 'options' must be an affirmative statement the user can click, never another question. Good options: \"Start a complex plan\", \"Respond with a cheerful tone\". Bad options: \"Do you want a plan?\", \"¿Quieres que revise tus gustos?\". Do not assume or guess if key details are missing."
		systemPrefix = append(systemPrefix, ollama.Message{
			Role:    "system",
			Content: clarificationReinforce,
		})

		// Inject reinforcement for plan confirmation
		planMode := a.config().PlanConfirmation
		if planMode == "" {
			planMode = "smart"
		}
		hasActivePlanSteps := false
		activePlan, _ := a.registry.ActiveSessionPlan()
		if activePlan != nil && activePlan.Status == "active" {
			hasActivePlanSteps = activePlan.Completed < len(activePlan.Steps)
			currentIdx := activePlan.Completed
			if currentIdx >= len(activePlan.Steps) {
				currentIdx = len(activePlan.Steps) - 1
			}
			currentStep := activePlan.Steps[currentIdx]
			planReinforce := fmt.Sprintf("An approved execution plan is already active.\nSummary: %s\nCurrent step %d of %d: %s\nDo NOT call present_plan again. Execute the current step using the appropriate tools, then call complete_plan_step exactly once when the full top-level step is finished before moving to the next step. Do NOT respond with promises such as \"I will proceed\", \"I will investigate\", or \"I will do this later\" unless you either call a tool now or call defer_plan_continuation with a clear user-facing follow-up message.",
				activePlan.Summary, activePlan.Completed+1, len(activePlan.Steps), currentStep)
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: planReinforce,
			})
		} else if planMode == "always" {
			planReinforce := "Before executing ANY multi-step task or calling any other tools (like editing files, running commands, or search), you MUST first call the 'present_plan' tool with a summary and ordered list of steps to present your plan for user approval. Do NOT start executing steps until the user has approved the plan. After approval, each listed step may require several sub-actions. Call 'complete_plan_step' exactly once only when a full top-level plan step is finished and you are ready to move to the next step; do not call it for sub-actions."
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: planReinforce,
			})
		} else if planMode == "smart" {
			planReinforce := "For complex tasks requiring multiple steps, file modifications, or tool sequences, you SHOULD call the 'present_plan' tool to present your plan to the user for approval before calling other tools. DO NOT call present_plan for simple tasks, simple questions, weather retrieval, or when you only need to run a single tool call (e.g., calling web_search to find the weather or read_file to read a document). In those cases, call the tool directly without presenting a plan first."
			systemPrefix = append(systemPrefix, ollama.Message{
				Role:    "system",
				Content: planReinforce,
			})
		}

		// 2. Build system instructions incorporating Todo checklists, goals, and skills
		activeMessages := make([]ollama.Message, 0, len(systemPrefix)+len(messages))
		activeMessages = append(activeMessages, systemPrefix...)
		activeMessages = append(activeMessages, messages...)

		// --- CONTEXT OPTIMIZATION CHECK ---
		formattedActiveMessages := make([]ollama.Message, len(activeMessages))
		for idx, msg := range activeMessages {
			formattedActiveMessages[idx] = msg
			if msg.Role == "assistant" && msg.Thinking != "" && !strings.Contains(msg.Content, "<think>") {
				formattedActiveMessages[idx].Content = fmt.Sprintf("<think>\n%s\n</think>\n%s", msg.Thinking, msg.Content)
			}
		}

		limit := a.getContextLimit(ctx, model)
		totalTokens := estimateTokens(formattedActiveMessages)
		threshold := int(float64(limit) * 0.9)

		if limit > 0 && totalTokens >= threshold {
			// Find the last user message in 'messages' to split history
			lastUserIndex := -1
			for idx := len(messages) - 1; idx >= 0; idx-- {
				if messages[idx].Role == "user" {
					lastUserIndex = idx
					break
				}
			}

			if lastUserIndex > 0 {
				startTime := time.Now()
				tokensBefore := totalTokens
				percentBefore := (float64(tokensBefore) / float64(limit)) * 100

				if handler != nil {
					handler.OnContextOptimizationStart(tokensBefore, percentBefore)
				}

				// Run optimization/summarization
				modelToUse := a.config().OllamaModelSubagent
				if strings.TrimSpace(modelToUse) == "" {
					modelToUse = model
				}

				summaryPrompt := ollama.Message{
					Role:    "system",
					Content: "Please summarize the conversation history above. Focus on goals achieved, decisions made, the state of any files modified, and key context. Keep the summary extremely concise but detailed enough for an AI agent to continue the work. Respond with ONLY the summary, no introductory or concluding remarks.",
				}

				summarizingMessages := make([]ollama.Message, lastUserIndex)
				for idx := 0; idx < lastUserIndex; idx++ {
					msg := messages[idx]
					if msg.Role == "assistant" && msg.Thinking != "" && !strings.Contains(msg.Content, "<think>") {
						msg.Content = fmt.Sprintf("<think>\n%s\n</think>\n%s", msg.Thinking, msg.Content)
					}
					summarizingMessages[idx] = msg
				}

				summaryReq := ollama.ChatRequest{
					Model:    modelToUse,
					Messages: append(summarizingMessages, summaryPrompt),
				}

				summaryResp, err := a.client.Chat(ctx, summaryReq)
				if err != nil && modelToUse != model {
					log.Printf("[Agent Run] Context optimization failed using subagent model %q: %v. Falling back to main model %q", modelToUse, err, model)
					summaryReq.Model = model
					summaryResp, err = a.client.Chat(ctx, summaryReq)
				}
				if err == nil && strings.TrimSpace(summaryResp.Message.Content) != "" {
					summaryText := strings.TrimSpace(summaryResp.Message.Content)
					summaryMsg := ollama.Message{
						Role:    "system",
						Content: fmt.Sprintf("This is a summary of the optimized previous context:\n%s", summaryText),
					}

					// Update messages slice: replace messages[0 : lastUserIndex] with summaryMsg
					messages = append([]ollama.Message{summaryMsg}, messages[lastUserIndex:]...)

					// Notify handler to update recorder/session store
					if handler != nil {
						handler.OnContextOptimized(messages, summaryMsg.Content, len(messages)-1)
					}

					// Rebuild activeMessages with optimized messages
					activeMessages = make([]ollama.Message, 0, len(systemPrefix)+len(messages))
					activeMessages = append(activeMessages, systemPrefix...)
					activeMessages = append(activeMessages, messages...)

					// Calculate tokens after optimization
					newFormattedActive := make([]ollama.Message, len(activeMessages))
					for idx, msg := range activeMessages {
						newFormattedActive[idx] = msg
						if msg.Role == "assistant" && msg.Thinking != "" && !strings.Contains(msg.Content, "<think>") {
							newFormattedActive[idx].Content = fmt.Sprintf("<think>\n%s\n</think>\n%s", msg.Thinking, msg.Content)
						}
					}
					tokensAfter := estimateTokens(newFormattedActive)
					percentAfter := (float64(tokensAfter) / float64(limit)) * 100
					durationSeconds := time.Since(startTime).Seconds()

					if handler != nil {
						handler.OnContextOptimizationEnd(tokensAfter, percentAfter, durationSeconds)
					}
				} else if err != nil {
					log.Printf("[Agent Run] Context optimization failed: %v", err)
				}
			}
		}

		// 3. Prepare the request
		requestMessages := make([]ollama.Message, len(activeMessages))
		for idx, msg := range activeMessages {
			requestMessages[idx] = msg
			if msg.Role == "assistant" && msg.Thinking != "" && !strings.Contains(msg.Content, "<think>") {
				requestMessages[idx].Content = fmt.Sprintf("<think>\n%s\n</think>\n%s", msg.Thinking, msg.Content)
			}
		}

		req := ollama.ChatRequest{
			Model:    model,
			Messages: requestMessages,
			Think:    think,
		}
		
		// Set optimal options to prevent context and prediction truncation
		options := map[string]any{
			"num_ctx":     numCtx,
			"num_predict": numPredict,
			"temperature": 0.2,
		}
		a.mu.RLock()
		for k, v := range a.options {
			options[k] = v
		}
		a.mu.RUnlock()
		req.Options = options

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
				var result string
				var terr error
				if toolName == "complete_plan_step" && !planStepHasAction {
					terr = fmt.Errorf("you must execute at least one action tool for the current plan step before calling complete_plan_step")
					result = fmt.Sprintf("Error: %v", terr)
				} else {
					result, terr = a.registry.Execute(ctx, call)
				}
				if terr != nil {
					result = fmt.Sprintf("Error: %v", terr)
				}

				// Proactive error recovery/assistance
				lowerResult := strings.ToLower(result)
				if terr != nil || strings.HasPrefix(result, "Error") {
					// 1. File Not Found Assistance
					if strings.Contains(lowerResult, "not found") || strings.Contains(lowerResult, "no such file") {
						var rawPath string
						if toolName == "read_file" {
							rawPath, _ = params["path"].(string)
						} else if toolName == "Edit" || toolName == "Write" {
							rawPath, _ = params["file_path"].(string)
						}
						if rawPath != "" {
							if suggs := a.paths.FindSuggestions(rawPath); len(suggs) > 0 {
								var sb strings.Builder
								sb.WriteString(result)
								sb.WriteString("\n\n[PROACTIVE SYSTEM ASSISTANCE: The requested file was not found. Here are some files in your workspace with similar names that you might have meant to access:]")
								for _, s := range suggs {
									rel, err := filepath.Rel(a.config().Workspace, s)
									if err == nil && !strings.HasPrefix(rel, "..") {
										fmt.Fprintf(&sb, "\n- %s", rel)
									} else {
										fmt.Fprintf(&sb, "\n- %s", s)
									}
								}
								sb.WriteString("\n[Please check the file name and verify with the suggestions above before trying again.]")
								result = sb.String()
							}
						}
					}

					// 2. Edit Match Failure Assistance
					if toolName == "Edit" && strings.Contains(result, "old_string not found") {
						filePath, _ := params["file_path"].(string)
						if filePath != "" {
							content, readErr := tools.ReadFile(a.config().Workspace, filePath)
							if readErr == nil {
								lines := strings.Split(content, "\n")
								var sb strings.Builder
								sb.WriteString(result)
								sb.WriteString("\n\n[PROACTIVE SYSTEM ASSISTANCE: The target text could not be located in the file. Here is the current content of the file to help you find the correct target block for replacement:]\n")
								if len(lines) <= 250 {
									sb.WriteString("```\n")
									sb.WriteString(content)
									sb.WriteString("\n```")
								} else {
									sb.WriteString("File is too long to display fully. Here are the first 150 lines:\n```\n")
									for idx, line := range lines {
										if idx >= 150 {
											break
										}
										fmt.Fprintf(&sb, "%d: %s\n", idx+1, line)
									}
									sb.WriteString("\n```\n")

									oldStringVal, _ := params["old_string"].(string)
									oldLines := strings.Split(oldStringVal, "\n")
									if len(oldLines) > 0 && strings.TrimSpace(oldLines[0]) != "" {
										targetLineNorm := strings.TrimSpace(strings.ToLower(oldLines[0]))
										var matchLines []string
										for idx, line := range lines {
											if strings.Contains(strings.ToLower(line), targetLineNorm) {
												startLine := max(0, idx-5)
												endLine := min(len(lines)-1, idx+5)
												var contextBlock strings.Builder
												for l := startLine; l <= endLine; l++ {
													fmt.Fprintf(&contextBlock, "  Line %d: %s\n", l+1, lines[l])
												}
												matchLines = append(matchLines, contextBlock.String())
												if len(matchLines) >= 3 {
													break
												}
											}
										}
										if len(matchLines) > 0 {
											sb.WriteString("\nPotential matches for your target text start line:\n")
											for _, mBlock := range matchLines {
												sb.WriteString(mBlock)
												sb.WriteString("\n")
											}
										}
									}
								}
								result = sb.String()
							}
						}
					}
				}

				// Check for repetitive loops.
				signature, label := sessions.FormatApprovalSignature(toolName, params, a.config().Workspace)
				key := toolName + ":" + signature
				toolCallCounts[key]++
				repeatCount := toolCallCounts[key]
				var repetitiveLoopErr error
				if repeatCount >= 5 {
					repetitiveLoopErr = fmt.Errorf("detected repetitive loop: %s called %d times without meaningful progress (%s)", toolName, repeatCount, label)
					result = fmt.Sprintf("%s\n\nError: %v", result, repetitiveLoopErr)
				} else if repeatCount >= 4 {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: You have called tool '%s' with the same normalized arguments %d times. You must justify why another identical execution is necessary or change approach before retrying.]", result, toolName, repeatCount)
				} else if repeatCount >= 3 {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: You have called tool '%s' with the identical arguments %d times. To avoid a repetitive loop, please check the file path, verify the contents of the file using read_file, or try a different approach.]", result, toolName, repeatCount)
				}

				// Remember observed paths
				isError := terr != nil || strings.HasPrefix(result, "Error")
				a.paths.RememberToolResult(toolName, params, result, isError)
				if !isError {
					switch toolName {
					case "complete_plan_step":
						planStepHasAction = false
						planTextOnlyRetries = 0
					case "present_plan", "ask_clarification", "defer_plan_continuation":
					default:
						planStepHasAction = true
					}
				}

				if handler != nil {
					handler.OnToolResult(toolName, result)
				}

				messages = append(messages, ollama.Message{
					Role:    "tool",
					Name:    toolName,
					Content: result,
				})
				if repetitiveLoopErr != nil {
					return messages, repetitiveLoopErr
				}
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

		// 10. Enforce active plan execution: refuse to end loop while plan steps remain.
		activePlan, _ = a.registry.ActiveSessionPlan()
		hasActivePlanSteps = activePlan != nil && activePlan.Status == "active" && activePlan.Completed < len(activePlan.Steps)
		if hasActivePlanSteps {
			if planTextOnlyRetries >= 5 {
				return messages, fmt.Errorf("agent stalled on plan step %d of %d: %s", activePlan.Completed+1, len(activePlan.Steps), activePlan.Steps[activePlan.Completed])
			}
			planTextOnlyRetries++
			currentStep := activePlan.Steps[activePlan.Completed]
			messages = append(messages, ollama.Message{
				Role: "system",
				Content: fmt.Sprintf("There is an approved plan with remaining steps (currently step %d of %d: %s). Continue executing the current step with tool calls — do not finish the turn with plain text or promises. Call complete_plan_step only after at least one action tool has been executed for this top-level step. If this work must happen later, call defer_plan_continuation and clearly notify the user.",
					activePlan.Completed+1, len(activePlan.Steps), currentStep),
			})
			continue
		}

		// No more tools, no pending Todos, and no remaining plan steps: complete task cleanly!
		completedCleanly = true
		break
	}

	if !completedCleanly {
		return messages, fmt.Errorf("agent exceeded maximum tool iterations (%d) before completing the turn", MaxIterations)
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

func (a *Agent) getContextLimit(ctx context.Context, model string) int64 {
	show, err := a.client.Show(ctx, model)
	if err == nil {
		for key, value := range show.ModelInfo {
			if strings.HasSuffix(key, ".context_length") {
				switch typed := value.(type) {
				case float64:
					return int64(typed)
				case int64:
					return typed
				case int:
					return int64(typed)
				}
			}
		}
	}
	return 2048
}

func estimateTokens(messages []ollama.Message) int {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Content)
		chars += len(msg.Thinking)
		chars += len(msg.Role)
		chars += len(msg.Name)
		for _, tc := range msg.ToolCalls {
			chars += len(tc.Function.Name)
			chars += len(tc.Function.Arguments)
		}
		if len(msg.Images) > 0 {
			chars += len(msg.Images) * 4000
		}
	}
	return (chars + 3) / 4
}
