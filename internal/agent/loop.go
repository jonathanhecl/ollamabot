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

// SubagentContext wraps ctx with a timeout derived from cfg.SubagentTimeoutMinutes.
// If the value is unset (0) or invalid, it defaults to 10 minutes.
func SubagentContext(ctx context.Context, cfg config.Config) (context.Context, context.CancelFunc) {
	minutes := cfg.SubagentTimeoutMinutes
	if minutes <= 0 {
		minutes = 10
	}
	return context.WithTimeout(ctx, time.Duration(minutes)*time.Minute)
}

type StreamHandler interface {
	OnThinking(delta string)
	OnContent(delta string)
	OnToolCall(call ollama.ToolCall)
	OnToolStart(name string, args any, source string)
	OnToolResult(name string, result string, source string)
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
	// Per-tool global call counts (across all argument variants). Used to cap
	// expensive network tools that the model can spin through on many different
	// URLs/queries without triggering the per-signature loop detector.
	toolGlobalCounts := make(map[string]int)

	cfg := a.config()
	limit := a.getContextLimit(ctx, model)

	// Use configurable context window, capped by the model's actual context_length.
	// If OllamaMaxContext is 0, use the model's full context_length (no artificial cap).
	numCtx := int(limit)
	if cfg.OllamaMaxContext > 0 && cfg.OllamaMaxContext < numCtx {
		numCtx = cfg.OllamaMaxContext
	}
	if numCtx < 2048 {
		numCtx = 2048
	}

	// Use configurable max tokens for generation. Default 16384 for autonomous agents.
	numPredict := cfg.OllamaMaxTokens
	if numPredict <= 0 {
		numPredict = 16384
	}

	// Find the current goal from the last user message
	var goal string
	var userTextSB strings.Builder
	for _, m := range messages {
		if m.Role == "user" {
			userTextSB.WriteString(m.Content)
			userTextSB.WriteString("\n")
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			goal = messages[i].Content
			break
		}
	}
	userText := userTextSB.String()
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

	// Automatic pre-fetch of relevant past session context based on user prompt
	var pastSessionsBlock string
	if a.registry != nil && a.registry.SessionStore() != nil {
		currentSessID := a.registry.SessionID()
		pastSessionsBlock = buildPastSessionsContext(a.registry.SessionStore(), goal, currentSessID)
	}

	emptyChatErrRetries := 0
	planStepHasAction := false
	planTextOnlyRetries := 0
	todoTextOnlyRetries := 0
	completedCleanly := false
	contextOptimizationFailed := false
	lengthRetries := 0

	// Track filenames returned by MCP tools during this turn, so we can warn
	// the model when it tries to access them via workspace tools (read_file,
	// list_files, etc.) instead of the matching MCP tool.
	mcpReturnedNames := make(map[string]bool)

	// --- STATIC SYSTEM PREFIX (computed once, reused every iteration) ---
	// These messages don't change between loop iterations, so we build them
	// once before the loop instead of re-reading files from disk every turn.
	staticPrefix := buildStaticSystemPrefix(a, goal, recalledMemoriesBlock, skillsBlock, pastSessionsBlock)

	for i := 0; i < MaxIterations; i++ {
		// --- DYNAMIC SYSTEM PREFIX (rebuilt each iteration) ---
		// Only the date/time, todo progress, and plan reinforcement actually
		// change between iterations.
		var dynamicPrefix []ollama.Message

		// Inject current date and time so the model always knows the temporal context
		now := time.Now()
		_, offset := now.Zone()
		sign := "+"
		if offset < 0 {
			sign = "-"
			offset = -offset
		}
		utcOffset := fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
		timeStr := now.Format("Monday, January 2, 2006 at 3:04 PM")
		dynamicPrefix = append(dynamicPrefix, ollama.Message{
			Role:    "system",
			Content: fmt.Sprintf("Current date and time: %s (%s)", timeStr, utcOffset),
		})

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
			dynamicPrefix = append(dynamicPrefix, ollama.Message{
				Role:    "system",
				Content: todoNote,
			})
		}

		// Inject reinforcement for plan confirmation (changes with plan state)
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
			dynamicPrefix = append(dynamicPrefix, ollama.Message{
				Role:    "system",
				Content: planReinforce,
			})
		} else if planMode == "always" {
			planReinforce := "Before executing ANY multi-step task or calling any other tools (like editing files, running commands, or search), you MUST first call the 'present_plan' tool with a summary and ordered list of steps to present your plan for user approval. Do NOT start executing steps until the user has approved the plan. After approval, each listed step may require several sub-actions. Call 'complete_plan_step' exactly once only when a full top-level plan step is finished and you are ready to move to the next step; do not call it for sub-actions."
			dynamicPrefix = append(dynamicPrefix, ollama.Message{
				Role:    "system",
				Content: planReinforce,
			})
		} else if planMode == "smart" {
			planReinforce := "For complex tasks requiring multiple steps, file modifications, or tool sequences, you SHOULD call the 'present_plan' tool to present your plan to the user for approval before calling other tools. DO NOT call present_plan for simple tasks, simple questions, weather retrieval, or when you only need to run a single tool call (e.g., calling web_search to find the weather or read_file to read a document). In those cases, call the tool directly without presenting a plan first."
			dynamicPrefix = append(dynamicPrefix, ollama.Message{
				Role:    "system",
				Content: planReinforce,
			})
		}

		// 2. Build active messages: static prefix + dynamic prefix + conversation
		systemPrefixLen := len(staticPrefix) + len(dynamicPrefix)
		activeMessages := make([]ollama.Message, 0, systemPrefixLen+len(messages))
		activeMessages = append(activeMessages, staticPrefix...)
		activeMessages = append(activeMessages, dynamicPrefix...)
		activeMessages = append(activeMessages, messages...)

		// --- CONTEXT OPTIMIZATION CHECK ---
		formattedActiveMessages := make([]ollama.Message, len(activeMessages))
		for idx, msg := range activeMessages {
			formattedActiveMessages[idx] = msg
			if msg.Role == "assistant" && msg.Thinking != "" && !strings.Contains(msg.Content, "<think>") {
				formattedActiveMessages[idx].Content = fmt.Sprintf("<think>\n%s\n</think>\n%s", msg.Thinking, msg.Content)
			}
		}

		totalTokens := estimateTokens(formattedActiveMessages)
		threshold := int(float64(limit) * 0.9)

		if limit > 0 && totalTokens >= threshold && !contextOptimizationFailed {
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
					activeMessages = make([]ollama.Message, 0, len(staticPrefix)+len(dynamicPrefix)+len(messages))
					activeMessages = append(activeMessages, staticPrefix...)
					activeMessages = append(activeMessages, dynamicPrefix...)
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
					contextOptimizationFailed = true
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
		doneReason := ""
		var contentFilter StreamThinkingFilter
		var lastChunk ollama.ChatResponse

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
				doneReason = chunk.DoneReason
				lastChunk = chunk
				log.Printf("[Agent] Stream done: reason=%s eval_count=%d total_duration=%dms", chunk.DoneReason, chunk.EvalCount, chunk.TotalDuration/1e6)
				if chunk.DoneReason == "length" {
					log.Printf("[Agent] WARNING: Response truncated due to token limit (num_predict=%d). Consider increasing OLLAMA_MAX_TOKENS.", numPredict)
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

		// Emit any content held back by the thinking-token filter before closing the turn.
		// This content belongs to the current turn, not the next one.
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
					handler.OnDone(lastChunk)
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
		if handler != nil {
			handler.OnDone(lastChunk)
		}

		// 6b. Handle length-truncated responses: the model hit num_predict mid-generation.
		// If there are no tool calls to process, nudge the model to continue rather than
		// accepting incomplete content as the final answer.
		if doneReason == "length" && len(toolCalls) == 0 {
			if lengthRetries < 2 {
				lengthRetries++
				messages = append(messages, ollama.Message{
					Role:    "system",
					Content: "Your previous response was truncated due to token limit. Please continue from where you left off and complete your response.",
				})
				continue
			}
			log.Printf("[Agent] WARNING: Response still truncated after %d continuation retries. Accepting partial response.", lengthRetries)
		}

		// 7. Execute tool calls if any
		if len(toolCalls) > 0 {
			emptyChatErrRetries = 0
			todoTextOnlyRetries = 0

			// --- Phase 1: Pre-execution (sequential) ---
			// Prepare each tool call: parse params, rescue paths, redirect
			// web_search -> fetch_webpage, and classify as parallel-safe.
			type preparedCall struct {
				call              ollama.ToolCall
				toolName          string
				params            map[string]any
				toolSource        string
				redirectedToFetch bool
				parallelSafe      bool
			}
			prepared := make([]preparedCall, len(toolCalls))
			allParallelSafe := len(toolCalls) > 1

			for idx, call := range toolCalls {
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

				// Guardrail: if the model tries to web_search for a URL the user
				// already provided, redirect the call to fetch_webpage directly.
				redirectedToFetch := false
				if toolName == "web_search" {
					if q, _ := params["query"].(string); q != "" {
						if u, ok := redirectSearchToFetch(userText, q); ok {
							log.Printf("[guardrail] web_search -> fetch_webpage (user already provided URL): %s", u)
							toolName = "fetch_webpage"
							params = map[string]any{"url": u}
							call.Function.Name = toolName
							call.Function.Arguments, _ = json.Marshal(params)
							redirectedToFetch = true
						}
					}
				}

				toolSource := a.registry.GetToolSource(toolName)
				ps := isParallelSafeTool(toolName, params, toolSource)
				if !ps {
					allParallelSafe = false
				}

				prepared[idx] = preparedCall{
					call:              call,
					toolName:          toolName,
					params:            params,
					toolSource:        toolSource,
					redirectedToFetch: redirectedToFetch,
					parallelSafe:      ps,
				}
			}

			// --- Phase 2+3: Execution and Post-processing ---
			// If all calls are parallel-safe read-only tools, execute them
			// concurrently with goroutines, then post-process in order.
			// Otherwise, execute AND post-process each call sequentially so
			// that state changes (e.g. planStepHasAction) are visible to
			// subsequent calls in the same batch.
			type execResult struct {
				result string
				terr   error
			}

			// postProcess handles all post-execution logic for a single tool
			// call result: error recovery, loop detection, path memory, etc.
			// Returns (abort, abortErr): abort is true if the loop should
			// stop immediately due to a repetitive loop detection.
			postProcess := func(pc preparedCall, er execResult) (bool, error) {
				result := er.result
				terr := er.terr
				toolName := pc.toolName
				params := pc.params
				toolSource := pc.toolSource

				if terr != nil {
					result = fmt.Sprintf("Error: %v", terr)
				}
				if pc.redirectedToFetch && terr == nil {
					result = "[SYSTEM NOTE: The user already provided this URL; it was fetched directly instead of searching.]\n\n" + result
				}

				// Track filenames returned by MCP tools so we can warn the
				// model if it later tries to access them via workspace tools.
				if toolSource != "internal" && terr == nil {
					collectMCPReturnedNames(result, mcpReturnedNames)
					if strings.Contains(result, "empty output or no results") {
						result = fmt.Sprintf("%s\n\n[SYSTEM NOTE: The MCP tool returned 0 results or empty output. Consider broadening your query parameters or checking server status with mcp_list_servers. Do NOT call local workspace tools (read_file, list_files) as a substitute for MCP searches unless requested by the user.]", result)
					}
				}

				// Guardrail: if the model calls a workspace file tool on a
				// filename that was returned by an MCP tool, warn it to use
				// the MCP tool instead. This catches the common confusion
				// where the model sees vault_list return "OpenAI.md" and then
				// tries read_file("workspace/OpenAI.md").
				if isWorkspaceFileTool(toolName) && terr != nil {
					if matched := matchMCPReturnedName(toolName, params, mcpReturnedNames); matched != "" {
						result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: The file '%s' was returned by an MCP tool earlier in this conversation. It lives inside the MCP service, NOT in the local workspace. Do NOT use %s on it — use the matching MCP read/list tool instead. Repeated workspace tool calls on MCP-returned files will not find them.]", result, matched, toolName)
					}
				}

				// Proactive error recovery/assistance
				lowerResult := strings.ToLower(result)
				if terr != nil || strings.HasPrefix(result, "Error") {
					// 1. File Not Found Assistance
					if strings.Contains(lowerResult, "not found") || strings.Contains(lowerResult, "no such file") {
						var rawPath string
						if toolName == "read_file" {
							rawPath, _ = params["path"].(string)
						} else if toolName == "edit_file" || toolName == "write_file" || toolName == "apply_diff" {
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
					if toolName == "edit_file" && strings.Contains(result, "old_string not found") {
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

				// Detect error states, denials/timeouts, and MCP schema validation errors early
				isError := terr != nil || strings.HasPrefix(result, "Error")
				isDeniedOrTimeout := strings.Contains(result, "Denied by user") || strings.Contains(result, "approval timeout") || strings.Contains(result, "approval failed")
				isMCPValidationError := strings.Contains(result, "MCP error -32602") || strings.Contains(result, "Input validation error")

				if isDeniedOrTimeout {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: Tool execution was DENIED or TIMED OUT by the user. Do NOT repeat the exact same tool call. Inform the user that the action was not approved and ask how to proceed.]", result)
				} else if isMCPValidationError {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: The MCP tool rejected the provided arguments (schema validation error -32602). Do NOT retry with identical arguments; verify tool parameter schema or try a different approach.]", result)
				}

				// Check for repetitive loops.
				signature, label := sessions.FormatApprovalSignature(toolName, params, a.config().Workspace)
				key := toolName + ":" + signature
				toolCallCounts[key]++
				repeatCount := toolCallCounts[key]
				toolGlobalCounts[toolName]++
				globalCount := toolGlobalCounts[toolName]
				var repetitiveLoopErr error

				// Determine abort threshold dynamically based on call health.
				// Errored, denied/timed out, or schema-invalid calls abort much earlier (2-3 retries)
				// to prevent wasting time in multi-minute prompt evaluation loops.
				isNoOpCall := isNoOpToolCall(toolName, params)
				isNetworkTool := isNetworkFetchTool(toolName)
				abortThreshold := 5
				if isDeniedOrTimeout || isMCPValidationError || isError {
					abortThreshold = 2
				} else if isNoOpCall || isNetworkTool {
					abortThreshold = 3
				}

				// Global per-tool cap: even with different arguments, calling
				// a network tool too many times in one turn means the model is
				// thrashing. Warn at networkWarnThreshold, abort at
				// networkAbortThreshold.
				networkWarnThreshold := 8
				networkAbortThreshold := 12
				if isNetworkTool && globalCount >= networkAbortThreshold {
					repetitiveLoopErr = fmt.Errorf("detected excessive network tool usage: %s called %d times total in this turn (across different URLs/queries). Stop fetching and synthesize an answer from the data you already have, or ask the user for clarification", toolName, globalCount)
					result = fmt.Sprintf("%s\n\nError: %v", result, repetitiveLoopErr)
				} else if isNetworkTool && globalCount >= networkWarnThreshold {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: You have called %s %d times total in this turn. You have enough data to answer — synthesize your response from what you already fetched. Further fetching will be blocked soon.]", result, toolName, globalCount)
				}

				if repetitiveLoopErr == nil && repeatCount >= abortThreshold {
					repetitiveLoopErr = fmt.Errorf("detected repetitive loop: %s called %d times without meaningful progress (%s)", toolName, repeatCount, label)
					result = fmt.Sprintf("%s\n\nError: %v", result, repetitiveLoopErr)
				} else if repetitiveLoopErr == nil && ((repeatCount >= 2 && isNoOpCall) || repeatCount >= 3 || (repeatCount >= 2 && isError)) {
					result = fmt.Sprintf("%s\n\n[SYSTEM WARNING: You have called tool '%s' with the identical arguments %d times. %s]", result, toolName, repeatCount, repetitiveLoopHint(toolName, a.registry))
				}

				// Remember observed paths
				a.paths.RememberToolResult(toolName, params, result, isError)
				if !isError {
					switch toolName {
					case "complete_plan_step":
						planStepHasAction = false
						planTextOnlyRetries = 0
					case "present_plan", "ask_clarification", "defer_plan_continuation":
					default:
						planStepHasAction = true
						planTextOnlyRetries = 0
					}
				}

				if handler != nil {
					handler.OnToolResult(toolName, result, toolSource)
				}

				messages = append(messages, ollama.Message{
					Role:    "tool",
					Name:    toolName,
					Content: result,
				})
				if repetitiveLoopErr != nil {
					return true, repetitiveLoopErr
				}
				return false, nil
			}

			if allParallelSafe {
				// Parallel execution: run all tools concurrently, then
				// post-process results in order.
				results := make([]execResult, len(prepared))
				var wg sync.WaitGroup
				for idx := range prepared {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						pc := prepared[i]
						if handler != nil {
							handler.OnToolStart(pc.toolName, pc.params, pc.toolSource)
						}
						r, err := a.registry.Execute(ctx, pc.call)
						results[i] = execResult{result: r, terr: err}
					}(idx)
				}
				wg.Wait()

				for idx, pc := range prepared {
					if abort, abortErr := postProcess(pc, results[idx]); abort {
						return messages, abortErr
					}
				}
			} else {
				// Sequential execution: execute AND post-process each call
				// one at a time so state changes are visible to subsequent
				// calls in the same batch.
				for _, pc := range prepared {
					if handler != nil {
						handler.OnToolStart(pc.toolName, pc.params, pc.toolSource)
					}
					var r string
					var err error
					if pc.toolName == "complete_plan_step" && !planStepHasAction {
						err = fmt.Errorf("you must execute at least one action tool for the current plan step before calling complete_plan_step")
						r = fmt.Sprintf("Error: %v", err)
					} else {
						r, err = a.registry.Execute(ctx, pc.call)
					}
					if abort, abortErr := postProcess(pc, execResult{result: r, terr: err}); abort {
						return messages, abortErr
					}
				}
			}

			// Continue with next loop turn so the LLM processes results
			continue
		}

		// 8. Handle empty completions: model returned no usable content and no
		// tool calls. This covers two scenarios:
		//   a) Content was purely thinking tokens (now empty after cleaning).
		//   b) Model returned a completely empty response (no content, no thinking).
		if strings.TrimSpace(cleanedText) == "" && len(toolCalls) == 0 {
			if emptyChatErrRetries < 2 {
				emptyChatErrRetries++
				messages = append(messages, ollama.Message{
					Role:    "system",
					Content: "Previous attempt returned an empty response. Please produce a visible text response or call a tool.",
				})
				continue
			}
			return messages, fmt.Errorf("agent returned empty response after %d retries", emptyChatErrRetries)
		}

		// 9. Enforce Todo Completion: refuse to end loop if Todos are pending
		if hasPending {
			if todoTextOnlyRetries >= 5 {
				return messages, fmt.Errorf("agent stalled with pending TODO items after %d text-only retries", todoTextOnlyRetries)
			}
			todoTextOnlyRetries++
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

func buildPastSessionsContext(store *sessions.Store, userPrompt string, currentSessionID string) string {
	if store == nil {
		return ""
	}
	userPrompt = strings.TrimSpace(userPrompt)
	if len(userPrompt) < 10 {
		return ""
	}

	words := strings.Fields(strings.ToLower(userPrompt))
	var tokens []string
	stopwords := map[string]bool{
		"para": true, "como": true, "hacer": true, "sobre": true, "esta": true, "este": true,
		"esto": true, "con": true, "que": true, "del": true, "los": true, "las": true, "una": true,
		"uno": true, "unas": true, "unos": true, "por": true, "donde": true, "quien": true,
		"cual": true, "cuando": true, "the": true, "and": true, "for": true, "with": true,
		"this": true, "that": true, "from": true, "about": true, "what": true, "how": true,
		"hola": true, "buenas": true, "please": true, "help": true, "ayuda": true,
	}
	for _, w := range words {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
		})
		if len(w) >= 4 && !stopwords[w] {
			tokens = append(tokens, w)
		}
	}

	if len(tokens) == 0 {
		return ""
	}

	list, err := store.List()
	if err != nil || len(list) <= 1 {
		return ""
	}

	type match struct {
		id    string
		title string
		date  string
		score int
	}

	var matches []match
	for _, item := range list {
		if currentSessionID != "" && item.ID == currentSessionID {
			continue
		}
		score := 0
		lowerTitle := strings.ToLower(item.Title)
		lowerGoal := strings.ToLower(item.GoalObjective)

		for _, tok := range tokens {
			if strings.Contains(lowerTitle, tok) {
				score += 3
			}
			if strings.Contains(lowerGoal, tok) {
				score += 2
			}
		}

		if score > 0 {
			t := item.LastMessageAt
			if t.IsZero() {
				t = item.UpdatedAt
			}
			if t.IsZero() {
				t = item.CreatedAt
			}
			matches = append(matches, match{
				id:    item.ID,
				title: item.Title,
				date:  t.Format("2006-01-02"),
				score: score,
			})
		}
	}

	if len(matches) == 0 {
		return ""
	}

	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].score > matches[i].score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	if len(matches) > 3 {
		matches = matches[:3]
	}

	var sb strings.Builder
	sb.WriteString("## Automatically Recalled Context from Relevant Past Sessions\n")
	sb.WriteString("Based on the user's prompt, the following past sessions appear relevant:\n")
	for _, m := range matches {
		title := m.title
		if title == "" || sessions.IsDefaultTitle(title) {
			title = "(Untitled)"
		}
		fmt.Fprintf(&sb, "- Past Session ID: %s | Title: %q | Date: %s\n", m.id, title, m.date)
	}
	sb.WriteString("You may use session_read(session_id) if you need the full transcript.")
	return sb.String()
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

// buildStaticSystemPrefix constructs the system messages that don't change
// between loop iterations: SOUL.md, USER_PROFILE.md, memory/image/MCP
// instructions, recalled memories, skills, goal reinforcement, and
// clarification reinforcement. Computing these once per Run avoids
// re-reading files from disk up to 50 times.
func buildStaticSystemPrefix(a *Agent, goal, recalledMemoriesBlock, skillsBlock, pastSessionsBlock string) []ollama.Message {
	var prefix []ollama.Message

	// Load SOUL.md once
	if soul, err := LoadSoul(); err == nil && soul != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: soul,
		})
	}

	// Load USER_PROFILE.md once
	if profile, err := LoadUserProfile(); err == nil && profile != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: "# User Profile & Preferences\n" + profile,
		})
	}

	// Memory tools instruction (static)
	if a.config().OllamaModelEmbed != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: "You have access to long-term memory tools (memory_add, memory_search, memory_delete, memory_list). Manage your own memory proactively:\n\n## What to Store\n- Permanent user preferences, personality traits, and communication style.\n- Technical decisions and architectural choices with rationale.\n- Lessons learned from debugging: error patterns, root causes, and working solutions.\n- Workspace-specific setups: missing dependencies, required env vars, unique build steps.\n- Durable facts about the user or project that apply across sessions.\n- Write self-contained, descriptive entries with enough context (technologies, file paths, exact solution) to be useful in isolation.\n\n## What NOT to Store\n- Current dates, times, or temporal context (the system already injects this).\n- Greetings, acknowledgments, or conversational filler.\n- Transient task state or progress (e.g. 'currently working on X', 'step 2 done').\n- Information that only applies to the current session.\n- Trivial or obvious facts that any model would know.\n- Anything you would not search for in a future conversation.\n\n## Rules\n- Before adding any memory, ask yourself: 'Will this be useful in a future session?' If no, do not store it.\n- Always search memory first (memory_search) before adding new memories.\n- Delete outdated or incorrect information using memory_delete.\n- Review stored memories with memory_list before deciding what to add, update, or remove.\n- Consolidate & Deduplicate: ALWAYS search for related facts first. If you learn updated information, DELETE the old version BEFORE adding the new one. Do not store near-identical or overlapping facts.\n- Lessons Learned: When you solve a difficult error or discover workspace-specific setups, store a concise 'lesson learned' memory so you do not repeat the mistake.",
		})
	}

	// Image generation instruction (static)
	if strings.TrimSpace(a.config().OllamaModelImage) != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: "You have access to image generation via the `generate_image` tool. When the user requests image creation (e.g., 'generate an image of...', 'create a picture of...', 'draw...', 'imagine...'), use this tool. Choose appropriate resolution based on context: 512x512 for standard square images, 1024x512 for landscape, 512x1024 for portrait. You can also specify custom smaller or aspect-ratio dimensions (like 64, 128, 256, etc.) directly when generating specific UI assets like icons, buttons, or logos. Important: The prompt passed to the generate_image tool must be in English for the best results, so you must translate the user's prompt to detailed, descriptive English if it is in another language. Do NOT output the generated image filename, path, or reference (e.g. do not say 'Reference: generated_...' or 'Referencia: ...') in your response to the user, as the user interface automatically renders the generated image bubble under your message. Simply confirm that the image is ready.",
		})
	}

	// MCP capability instruction (static + dynamic active server status)
	if a.registry != nil && a.registry.MCPManager() != nil {
		mcpContent := "You have access to tools from configured MCP (Model Context Protocol) servers. These tools are already listed among your available functions. Call the exposed MCP tool functions directly by name with the required arguments.\n\n" +
			"## CRITICAL: MCP tools vs workspace tools\n" +
			"Files, notes, records, or entries returned by MCP tools (e.g. vault_list, vault_read) live INSIDE the external service (Obsidian vault, database, remote API), NOT in the local workspace folder. The local workspace and the MCP service are two completely separate storage locations.\n" +
			"- To READ content that an MCP tool listed: use the matching MCP read tool (e.g. vault_read, get_note), NOT read_file.\n" +
			"- To LIST entries in an MCP service: use the MCP list tool (e.g. vault_list), NOT list_files.\n" +
			"- To WRITE/UPDATE content in an MCP service: use the matching MCP write tool, NOT write_file or edit_file.\n" +
			"- NEVER call read_file, list_files, write_file, edit_file, or apply_diff on paths or filenames that came from an MCP tool result. Those files do not exist in the workspace; calling workspace tools on them will fail and loop.\n" +
			"- If an MCP tool returns a filename like 'OpenAI.md', do NOT assume it lives under the workspace path. It lives inside the MCP service and must be accessed via MCP tools only.\n\n" +
			"## Transport\n" +
			"Do NOT use execute_command, shell, curl, wget, or fetch_webpage to manually query MCP transport endpoints (e.g., URLs ending in /mcp/, /mcp, /sse, /messages, or the Obsidian Local REST API). The MCP client handles all transport communication automatically.\n\n" +
			"## Failures\n" +
			"If an MCP tool fails or a server is not running, use mcp_list_servers to check status and report the issue instead of probing the endpoint manually. When the user explicitly asks for an action to be done through an MCP server (e.g., publishing or saving into that service) and the server is unreachable or the tool fails, STOP and tell the user the server is unavailable: do NOT substitute the action with workspace file writes or any other local workaround unless the user explicitly approves that fallback."

		if status, err := a.registry.MCPManager().GetServersStatus(); err == nil && len(status) > 0 {
			var sb strings.Builder
			sb.WriteString("\n\n## Connected MCP Servers and Available Tools\n")
			for srvName, srv := range status {
				st := srv.Status
				if srv.Degraded {
					st = "degraded (unreachable, cached tools)"
				}
				if srv.Error != "" {
					st = fmt.Sprintf("error (%s)", srv.Error)
				}
				toolNames := make([]string, 0, len(srv.Tools))
				for _, t := range srv.Tools {
					toolNames = append(toolNames, t.Name)
				}
				fmt.Fprintf(&sb, "- Server %q [%s]: %s\n", srvName, st, strings.Join(toolNames, ", "))
			}
			mcpContent += sb.String()
		}

		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: mcpContent,
		})
	}

	// Session history access instruction
	if a.registry != nil && a.registry.SessionStore() != nil {
		prefix = append(prefix, ollama.Message{
			Role: "system",
			Content: "## Past Session History & Digest Tools\n" +
				"You have access to tools to search, consult, digest, and export previous chat sessions:\n" +
				"- `sessions_list`: List previous chat sessions by date range (`date_from`, `date_to` in YYYY-MM-DD format, or `since_days`) or keyword (`query`).\n" +
				"- `sessions_search`: Search across previous chat session message histories for specific keywords or topics.\n" +
				"- `session_read`: Read the message transcript (user and assistant turns) of a specific past session by `session_id`.\n" +
				"- `sessions_digest`: Retrieve executive daily summaries / digests of past chat sessions over a period of time (`since_days` or date range).\n" +
				"- `session_export`: Export a past session transcript to a Markdown report file in the workspace (`output_path`).\n" +
				"Use these tools when the user asks to summarize past discussions, recall what was talked about in prior chats, export reports, or look up previous sessions.",
		})
	}

	// Recalled memories (static for this Run)
	if recalledMemoriesBlock != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: recalledMemoriesBlock,
		})
	}

	// Recalled past sessions (static for this Run)
	if pastSessionsBlock != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: pastSessionsBlock,
		})
	}

	// Skills block (static for this Run)
	if skillsBlock != "" {
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: "# Loaded Custom Skills\n\n" + skillsBlock,
		})
	}

	// Goal reinforcement (static for this Run)
	if goal != "" {
		goalReinforce := fmt.Sprintf("Your current user goal is:\n<<<USER_GOAL>>>\n%s\n<<<END_USER_GOAL>>>\nKeep executing until all steps are done. If an approved plan is active, do not stop until the plan is completed or you explicitly defer it with defer_plan_continuation and notify the user.", goal)
		prefix = append(prefix, ollama.Message{
			Role:    "system",
			Content: goalReinforce,
		})
	}

	// Clarification reinforcement (static)
	clarificationReinforce := "If the user's instructions are ambiguous, incomplete, or you need more details to plan or execute safely, you MUST use the 'ask_clarification' tool. Put the question only in 'question'. Every entry in 'options' must be an affirmative statement the user can click, never another question. Good options: \"Start a complex plan\", \"Respond with a cheerful tone\". Bad options: \"Do you want a plan?\", \"¿Quieres que revise tus gustos?\". Do not assume or guess if key details are missing."
	prefix = append(prefix, ollama.Message{
		Role:    "system",
		Content: clarificationReinforce,
	})

	return prefix
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
	return 8192
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

// isNoOpToolCall reports whether a tool call carries no meaningful parameters
// (e.g. list_files with empty args, or vault_list with empty args). Repeating
// such calls almost always indicates a stuck model, so the loop detector aborts
// earlier for them.
func isNoOpToolCall(toolName string, params map[string]any) bool {
	// Drop trivial keys that don't change the result of a "list" operation.
	meaningful := 0
	for k := range params {
		switch k {
		case "path", "recursive", "include":
			// For list_files, "path" defaults to "." and recursive/include are
			// optional filters. Only count them as meaningful if non-default.
			if k == "path" {
				if v, _ := params[k].(string); v != "" && v != "." && v != "./" {
					meaningful++
				}
			} else if k == "recursive" {
				if v, _ := params[k].(bool); v {
					meaningful++
				}
			} else if k == "include" {
				if v, _ := params[k].(string); v != "" {
					meaningful++
				}
			}
		default:
			// Any other key is considered meaningful.
			if v := params[k]; v != nil && v != "" && v != false {
				meaningful++
			}
		}
	}
	return meaningful == 0
}

// repetitiveLoopHint returns a tool-specific, actionable hint appended to the
// warning message when a tool is being called repeatedly. The old generic hint
// ("verify the contents of the file using read_file") was harmful for non-read
// tools because it pushed the model toward more file operations.
func repetitiveLoopHint(toolName string, registry *tools.Registry) string {
	// If the tool is an MCP tool, suggest checking the MCP server or using a
	// different MCP tool instead of looping.
	if registry != nil && registry.MCPManager() != nil && registry.MCPManager().HasTool(toolName) {
		lower := strings.ToLower(toolName)
		if strings.Contains(lower, "list") || strings.Contains(lower, "search") {
			return "You are repeating an MCP list/search tool with identical arguments. You already have the file listing above. To read a specific file from the list, use the matching MCP read/get tool (e.g. vault_get, vault_read) passing the file path as an argument. Do NOT repeat the list tool with identical arguments."
		}
		return "You are repeating the same MCP tool call. If the result is not what you expect, the MCP server may be misconfigured or the data may not exist. Use mcp_list_servers to check the server status, or try a different MCP tool. Do NOT fall back to workspace tools (list_files, read_file) — the data lives inside the MCP service, not in the workspace."
	}
	switch toolName {
	case "list_files", "search_files":
		return "Repeatedly listing the same directory will not produce different results. The files you are looking for may not exist in the workspace — if they came from an MCP tool (e.g. vault_list), use the matching MCP read tool instead. Otherwise, change your approach or ask the user for clarification."
	case "read_file":
		return "You already read this file. Re-reading it will return the same content. Use the content you already have, or if the file does not exist, check whether it lives in an MCP service (use the MCP read tool, not read_file)."
	case "web_search", "fetch_webpage":
		return "You already ran this search/fetch. Repeating it will return the same results. Use the data you already have, refine your query, or try a different source."
	default:
		return "To avoid a repetitive loop, change your arguments, use a different tool, or ask the user for clarification. Do NOT repeat the exact same call."
	}
}

// isWorkspaceFileTool reports whether a tool operates on the local workspace
// filesystem (as opposed to MCP services, web, memory, etc.).
func isWorkspaceFileTool(toolName string) bool {
	switch toolName {
	case "read_file", "list_files", "write_file", "edit_file", "apply_diff", "search_files":
		return true
	}
	return false
}

// isNetworkFetchTool reports whether a tool makes network requests. These are
// expensive and the model tends to cycle through many different URLs/queries
// without triggering the per-signature loop detector, so they get a lower
// per-signature threshold plus a global per-turn cap.
func isNetworkFetchTool(toolName string) bool {
	switch toolName {
	case "fetch_webpage", "web_search":
		return true
	}
	return false
}

// isParallelSafeTool reports whether a tool call can be executed concurrently
// with other tool calls in the same batch. Read-only tools that don't modify
// shared state (files, memory, plans, todos) are safe to parallelize. Tools
// that require approval, modify state, or interact with the user must run
// sequentially.
func isParallelSafeTool(toolName string, params map[string]any, toolSource string) bool {
	// MCP tools: only parallelize if the tool name suggests a read-only
	// operation. Conservative: default to sequential for MCP tools unless
	// the name clearly indicates read/list/get.
	if toolSource != "internal" {
		lower := strings.ToLower(toolName)
		if strings.Contains(lower, "list") || strings.Contains(lower, "read") || strings.Contains(lower, "get") || strings.Contains(lower, "search") {
			return true
		}
		return false
	}

	switch toolName {
	// Read-only workspace tools
	case "read_file", "list_files", "search_files", "list_code_definitions":
		return true
	// Network fetch tools (read-only by nature)
	case "fetch_webpage", "web_search":
		return true
	// Memory search is read-only
	case "memory_search", "memory_list":
		return true
	// Past session query tools are read-only
	case "sessions_list", "sessions_search", "session_read", "sessions_digest":
		return true
	// mcp_list_servers is read-only
	case "mcp_list_servers":
		return true
	}
	// Everything else (write_file, edit_file, apply_diff, execute_command,
	// memory_add, memory_delete, present_plan, ask_clarification,
	// complete_plan_step, defer_plan_continuation, generate_image,
	// send_files, mcp_add_server, mcp_delete_server, TodoWrite, etc.)
	// is sequential.
	return false
}

// collectMCPReturnedNames scans an MCP tool result for tokens that look like
// filenames (e.g. "OpenAI.md", "Noticias.md", "notes/foo.txt") and records
// their basenames so we can later warn the model if it tries to access them
// via workspace tools.
func collectMCPReturnedNames(result string, into map[string]bool) {
	if result == "" || into == nil {
		return
	}
	// Match tokens that look like filenames: word chars + optional path
	// separators + a dot + extension. Keep it conservative to avoid noise.
	// Examples: "OpenAI.md", "./notes/Foo.txt", "X_Posts/agent/README.md".
	for _, line := range strings.Split(result, "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			clean := strings.Trim(f, "\"'`.,;:()[]{}")
			if clean == "" {
				continue
			}
			// Strip leading bullets or list markers.
			clean = strings.TrimLeft(clean, "-*•·#> ")
			if clean == "" {
				continue
			}
			base := filepath.Base(clean)
			if hasFileExtension(base) && !isCommonNonFileToken(base) {
				into[strings.ToLower(base)] = true
			}
		}
	}
}

// hasFileExtension reports whether s looks like a filename with an extension.
func hasFileExtension(s string) bool {
	dot := strings.LastIndex(s, ".")
	if dot <= 0 || dot == len(s)-1 {
		return false
	}
	ext := s[dot+1:]
	if len(ext) > 6 {
		return false
	}
	for _, c := range ext {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// isCommonNonFileToken filters out tokens that have a dot+ext pattern but are
// not filenames (e.g. "v1.2", "127.0.0.1", "true.json" is fine but "no." is not).
func isCommonNonFileToken(s string) bool {
	// Pure numbers like "1.2" or IP-like "127.0.0.1"
	dots := strings.Count(s, ".")
	allDigitOrDot := true
	for _, c := range s {
		if !((c >= '0' && c <= '9') || c == '.') {
			allDigitOrDot = false
			break
		}
	}
	if allDigitOrDot && dots > 0 {
		return true
	}
	return false
}

// matchMCPReturnedName checks whether a workspace file tool call references a
// filename that was previously returned by an MCP tool. Returns the matched
// basename (lowercased) or "".
func matchMCPReturnedName(toolName string, params map[string]any, mcpNames map[string]bool) string {
	if len(mcpNames) == 0 {
		return ""
	}
	var pathStr string
	switch toolName {
	case "read_file", "list_files", "search_files":
		pathStr, _ = params["path"].(string)
	case "write_file", "edit_file", "apply_diff":
		pathStr, _ = params["file_path"].(string)
	}
	if pathStr == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(pathStr))
	if mcpNames[base] {
		return base
	}
	return ""
}
