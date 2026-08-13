package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// ProjectTodo represents a single step in a project
type ProjectTodo struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Status    string    `json:"status"` // "pending", "in_progress", "completed", "failed"
	Result    string    `json:"result,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Project represents the state of a mini-project in the workspace
type Project struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Goal        string        `json:"goal"`
	Status      string        `json:"status"` // "pending", "in_progress", "completed", "failed"
	Todos       []ProjectTodo `json:"todos"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	CurrentTask string        `json:"current_task,omitempty"`
}

// TaskNotificationFunc defines a callback for task success or failure
type TaskNotificationFunc func(proj Project, task ProjectTodo, err error)

// AutonomousManager manages background tickers and executions of workspace projects
type AutonomousManager struct {
	mu               sync.RWMutex
	cfgMgr           *config.Manager
	client           *ollama.Client
	memoryStore      *memory.Store
	isWorking        map[string]bool
	cancelFunc       context.CancelFunc
	tickerDone       chan struct{}
	interval         time.Duration
	onTaskCompletion TaskNotificationFunc
}

func (am *AutonomousManager) config() config.Config {
	return am.cfgMgr.Get()
}

// NewAutonomousManager creates a new instance of AutonomousManager
func NewAutonomousManager(cfg *config.Manager, client *ollama.Client, memoryStore *memory.Store) *AutonomousManager {
	return &AutonomousManager{
		cfgMgr:      cfg,
		client:      client,
		memoryStore: memoryStore,
		isWorking:   map[string]bool{},
		interval:    2 * time.Minute, // Default tick interval
	}
}

// Start starts the background heartbeat loop
func (am *AutonomousManager) Start(ctx context.Context) {
	am.mu.Lock()
	if am.cancelFunc != nil {
		am.mu.Unlock()
		return // Already running
	}
	ctx, cancel := context.WithCancel(ctx)
	am.cancelFunc = cancel
	am.tickerDone = make(chan struct{})
	am.mu.Unlock()

	ticker := time.NewTicker(am.interval)
	go func() {
		defer close(am.tickerDone)
		log.Println("[autonomous] Background manager heartbeat started")
		am.RecoverStaleTasks()
		for {
			select {
			case <-ticker.C:
				am.Tick(ctx)
			case <-ctx.Done():
				ticker.Stop()
				log.Println("[autonomous] Background manager heartbeat stopped")
				return
			}
		}
	}()
}

// RecoverStaleTasks scans all projects and resets tasks stuck in "in_progress"
// whose UpdatedAt is older than the configured staleness threshold. This handles
// the case where the process died mid-execution and the in-memory isWorking flag
// was lost, leaving a task permanently marked as in_progress.
func (am *AutonomousManager) RecoverStaleTasks() {
	threshold := am.config().AutonomousStaleTaskMinutes
	if threshold <= 0 {
		threshold = 30
	}
	cutoff := time.Now().Add(-time.Duration(threshold) * time.Minute)

	projects, err := am.ListProjects()
	if err != nil {
		log.Printf("[autonomous] RecoverStaleTasks: failed to list projects: %v", err)
		return
	}

	recovered := 0
	for _, proj := range projects {
		if proj.Status != "in_progress" {
			continue
		}
		dirty := false
		for i := range proj.Todos {
			t := &proj.Todos[i]
			if t.Status == "in_progress" && t.UpdatedAt.Before(cutoff) {
				t.Status = "pending"
				t.Result = fmt.Sprintf("Recovered from stale in_progress state (last updated %s). Previous result: %s", t.UpdatedAt.Format(time.RFC3339), t.Result)
				t.UpdatedAt = time.Now()
				recovered++
				dirty = true
			}
		}
		if dirty {
			proj.Status = "pending"
			proj.CurrentTask = ""
			if err := am.SaveProject(proj); err != nil {
				log.Printf("[autonomous] RecoverStaleTasks: failed to save project %s: %v", proj.ID, err)
			} else {
				log.Printf("[autonomous] RecoverStaleTasks: reset stale tasks in project %q", proj.Name)
			}
		}
	}
	if recovered > 0 {
		log.Printf("[autonomous] RecoverStaleTasks: recovered %d stale task(s) across %d project(s)", recovered, len(projects))
	}
}

// Stop stops the background heartbeat loop
func (am *AutonomousManager) Stop() {
	am.mu.Lock()
	if am.cancelFunc != nil {
		am.cancelFunc()
		am.mu.Unlock()
		<-am.tickerDone
		am.mu.Lock()
		am.cancelFunc = nil
		am.tickerDone = nil
	}
	am.mu.Unlock()
}

// SetInterval updates the tick interval
func (am *AutonomousManager) SetInterval(d time.Duration) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.interval = d
}

// UpdateClient swaps the Ollama client without stopping the ticker.
func (am *AutonomousManager) UpdateClient(c *ollama.Client) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.client = c
}

// UpdateMemoryStore swaps the memory store without stopping the ticker.
func (am *AutonomousManager) UpdateMemoryStore(ms *memory.Store) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.memoryStore = ms
}

// SetOnTaskCompletion registers a callback fired when an autonomous task completes or fails.
func (am *AutonomousManager) SetOnTaskCompletion(fn TaskNotificationFunc) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.onTaskCompletion = fn
}

// notifyTaskCompletion safely invokes the registered callback if any.
func (am *AutonomousManager) notifyTaskCompletion(proj Project, task ProjectTodo, err error) {
	am.mu.RLock()
	fn := am.onTaskCompletion
	am.mu.RUnlock()
	if fn != nil {
		fn(proj, task, err)
	}
}

// ListProjects scans the workspace root for folders containing "project.json"
func (am *AutonomousManager) ListProjects() ([]Project, error) {
	workspaceRoot := am.config().Workspace
	if _, err := os.Stat(workspaceRoot); os.IsNotExist(err) {
		return []Project{}, nil
	}

	files, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace root: %w", err)
	}

	var projects []Project
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		projPath := filepath.Join(workspaceRoot, f.Name(), "project.json")
		if _, err := os.Stat(projPath); err == nil {
			proj, err := am.LoadProject(f.Name())
			if err == nil {
				projects = append(projects, proj)
			}
		}
	}
	return projects, nil
}

// LoadProject loads a project's state from its project.json
func (am *AutonomousManager) LoadProject(id string) (Project, error) {
	projPath := filepath.Join(am.config().Workspace, id, "project.json")
	data, err := os.ReadFile(projPath)
	if err != nil {
		return Project{}, err
	}
	var proj Project
	if err := json.Unmarshal(data, &proj); err != nil {
		return Project{}, err
	}
	return proj, nil
}

// SaveProject saves the project state to project.json inside its folder
func (am *AutonomousManager) SaveProject(proj Project) error {
	proj.UpdatedAt = time.Now()
	projDir := filepath.Join(am.config().Workspace, proj.ID)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		return err
	}
	projPath := filepath.Join(projDir, "project.json")
	data, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(projPath, data, 0644)
}

// CreateProject initializes a project with a name, goal, and generates an initial TODO checklist
func (am *AutonomousManager) CreateProject(ctx context.Context, name, goal string) (Project, error) {
	// Generate clean ID from name
	id := strings.ToLower(name)
	id = strings.ReplaceAll(id, " ", "-")
	// Clean special chars
	var sb strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	id = sb.String()
	if id == "" {
		id = fmt.Sprintf("project-%d", time.Now().Unix())
	}

	// Avoid duplicates
	projDir := filepath.Join(am.config().Workspace, id)
	if _, err := os.Stat(projDir); err == nil {
		id = fmt.Sprintf("%s-%d", id, time.Now().Unix()%1000)
		projDir = filepath.Join(am.config().Workspace, id)
	}

	proj := Project{
		ID:        id,
		Name:      name,
		Goal:      goal,
		Status:    "pending",
		Todos:     []ProjectTodo{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Try generating sequential TODOs via Ollama Default Model
	todos, err := am.generateInitialTodos(ctx, name, goal)
	if err != nil {
		log.Printf("[autonomous] Warn: failed to generate structured TODOs: %v. Creating default fallback task.", err)
		proj.Todos = append(proj.Todos, ProjectTodo{
			ID:        "task-1",
			Content:   "Implement the project foundation based on the goal: " + goal,
			Status:    "pending",
			UpdatedAt: time.Now(),
		})
	} else {
		proj.Todos = todos
	}

	if err := am.SaveProject(proj); err != nil {
		return Project{}, err
	}

	return proj, nil
}

var initialTodosSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"todos": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"id", "content"},
			},
		},
	},
	"required": []string{"todos"},
}

type initialTodosResponse struct {
	Todos []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	} `json:"todos"`
}

type rawTodo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// generateInitialTodos calls Ollama to design a checklist of tasks
func (am *AutonomousManager) generateInitialTodos(ctx context.Context, name, goal string) ([]ProjectTodo, error) {
	model := am.config().OllamaDefaultModel
	if model == "" {
		return nil, fmt.Errorf("no default model configured")
	}

	prompt := fmt.Sprintf(`You are a technical product manager and software architect.
Analyze this mini-project request:
Project Name: "%s"
Project Goal: "%s"

Deconstruct this goal into a sequential checklist of 3 to 6 logical development tasks.
Each task must be concrete, specific, and actionable for an AI coding assistant.
Examples of tasks: "Create index.html layout and premium styled container with CSS", "Write game logic in app.js including scoring and collisions", "Add particle effects and high-score saving in local storage".

Respond with a JSON object containing the "todos" array of task objects conforming to the schema.`, name, goal)

	req := ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: prompt},
		},
		Format: initialTodosSchema, // Enforce structured output from Ollama with schema
	}

	var sb strings.Builder
	err := am.client.ChatStream(ctx, req, func(chunk ollama.ChatResponse) error {
		if chunk.Message.Content != "" {
			sb.WriteString(chunk.Message.Content)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	rawText := strings.TrimSpace(sb.String())
	// Strip markdown code fences if model returned them
	rawText = strings.TrimPrefix(rawText, "```json")
	rawText = strings.TrimPrefix(rawText, "```")
	rawText = strings.TrimSuffix(rawText, "```")
	rawText = strings.TrimSpace(rawText)

	var list []ProjectTodo

	// Attempt 1: Unmarshal matching the structured JSON schema
	var respObj initialTodosResponse
	if err := json.Unmarshal([]byte(rawText), &respObj); err == nil {
		for _, t := range respObj.Todos {
			list = append(list, ProjectTodo{
				ID:        t.ID,
				Content:   t.Content,
				Status:    "pending",
				UpdatedAt: time.Now(),
			})
		}
	} else {
		// Attempt 2: Fallback to direct array unmarshal (backwards compatibility / non-compliant models)
		var rawList []rawTodo
		if err := json.Unmarshal([]byte(rawText), &rawList); err == nil {
			for _, rt := range rawList {
				list = append(list, ProjectTodo{
					ID:        rt.ID,
					Content:   rt.Content,
					Status:    "pending",
					UpdatedAt: time.Now(),
				})
			}
		} else {
			log.Printf("[autonomous] Error parsing JSON todos: %v. Raw text was: %s", err, rawText)
			return nil, fmt.Errorf("failed to parse JSON todos: %w", err)
		}
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("empty todo list generated")
	}

	return list, nil
}

// Tick checks all projects and processes the next pending task of one active project
func (am *AutonomousManager) Tick(ctx context.Context) {
	am.mu.RLock()
	anyWorking := false
	for _, working := range am.isWorking {
		if working {
			anyWorking = true
			break
		}
	}
	am.mu.RUnlock()

	if anyWorking {
		// Already executing a task, wait for the next tick to avoid overloading Ollama
		return
	}

	projects, err := am.ListProjects()
	if err != nil {
		log.Printf("[autonomous] Tick error listing projects: %v", err)
		return
	}

	for _, proj := range projects {
		if proj.Status == "completed" || proj.Status == "failed" {
			continue
		}

		// Find the next task to process
		taskIdx := -1
		for i, todo := range proj.Todos {
			if todo.Status == "pending" || todo.Status == "in_progress" {
				taskIdx = i
				break
			}
		}

		// If no pending tasks are left, complete the project!
		if taskIdx == -1 {
			proj.Status = "completed"
			proj.CurrentTask = ""
			_ = am.SaveProject(proj)
			log.Printf("[autonomous] Project %q marked as COMPLETED!", proj.Name)
			continue
		}

		// Execute this project task
		go func(p Project, idx int) {
			if err := am.ExecuteTask(ctx, p.ID, idx); err != nil {
				log.Printf("[autonomous] Failed to execute task for project %s: %v", p.ID, err)
			}
		}(proj, taskIdx)

		// Process only one project per heartbeat to avoid overloading Ollama
		break
	}
}

type dummyStreamHandler struct{}

func (d *dummyStreamHandler) OnThinking(delta string)                                            {}
func (d *dummyStreamHandler) OnContent(delta string)                                             {}
func (d *dummyStreamHandler) OnToolCall(call ollama.ToolCall)                                    {}
func (d *dummyStreamHandler) OnToolStart(name string, args any, source string)                   {}
func (d *dummyStreamHandler) OnToolResult(name string, result string, source string)             {}
func (d *dummyStreamHandler) OnMediaPreProcessing(content string)                                {}
func (d *dummyStreamHandler) OnDone(resp ollama.ChatResponse)                                    {}
func (d *dummyStreamHandler) OnEvent(kind string, data any)                                      {}
func (d *dummyStreamHandler) OnContextOptimizationStart(tokensBefore int, percentBefore float64) {}
func (d *dummyStreamHandler) OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64) {
}
func (d *dummyStreamHandler) OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int) {
}

// autonomousStepCollector captures tool execution steps (and decision events)
// for an autonomous project task so they can be persisted into the heartbeat
// log in a structured, debug-friendly form.
type autonomousStepCollector struct {
	mu    sync.Mutex
	steps []sessions.Step
}

func newAutonomousStepCollector() *autonomousStepCollector {
	return &autonomousStepCollector{}
}

func (c *autonomousStepCollector) OnThinking(delta string) {}
func (c *autonomousStepCollector) OnContent(delta string)  {}
func (c *autonomousStepCollector) OnToolCall(call ollama.ToolCall) {}

func (c *autonomousStepCollector) OnToolStart(name string, args any, source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = append(c.steps, sessions.Step{
		Type:      "tool_exec",
		Name:      name,
		Source:    source,
		Arguments: args,
		Status:    "running",
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func (c *autonomousStepCollector) OnToolResult(name string, result string, source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.steps) - 1; i >= 0; i-- {
		if c.steps[i].Type == "tool_exec" && c.steps[i].Name == name && c.steps[i].Status == "running" {
			c.steps[i].Result = result
			c.steps[i].Status = "done"
			if strings.HasPrefix(strings.TrimSpace(result), "Error") {
				c.steps[i].Status = "error"
			}
			c.steps[i].DurationMs = sessions.StepDurationMs(c.steps[i].Timestamp)
			break
		}
	}
}

func (c *autonomousStepCollector) OnMediaPreProcessing(content string) {}
func (c *autonomousStepCollector) OnDone(resp ollama.ChatResponse)     {}

func (c *autonomousStepCollector) OnEvent(kind string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = append(c.steps, sessions.Step{
		Type:      "system_event",
		Name:      kind,
		Content:   sessions.EventContent(kind, data),
		Arguments: data,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func (c *autonomousStepCollector) OnContextOptimizationStart(tokensBefore int, percentBefore float64) {
	c.OnEvent("context_optimization_start", map[string]any{"tokens_before": tokensBefore, "percent_before": percentBefore})
}

func (c *autonomousStepCollector) OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64) {
	c.OnEvent("context_optimization_end", map[string]any{"tokens_after": tokensAfter, "percent_after": percentAfter, "duration_seconds": durationSeconds})
}

func (c *autonomousStepCollector) OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int) {
}

func (c *autonomousStepCollector) snapshot() []sessions.Step {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sessions.Step(nil), c.steps...)
}

// writeAutonomousStep appends a single structured step to a heartbeat log in a
// readable, debug-friendly markdown form (name, source, status, timing, args, result).
func writeAutonomousStep(sb *strings.Builder, idx int, step sessions.Step) {
	status := step.Status
	if status == "" {
		status = "done"
	}
	icon := "⚙️"
	if step.Type == "system_event" {
		icon = "ℹ️"
	}
	fmt.Fprintf(sb, "### %d. %s `%s` — %s", idx, icon, step.Name, status)
	if step.Source != "" && step.Source != "internal" {
		fmt.Fprintf(sb, " (source: %s)", step.Source)
	}
	if step.DurationMs > 0 {
		fmt.Fprintf(sb, " (%dms)", step.DurationMs)
	}
	fmt.Fprintf(sb, "\n\n")

	if step.Type == "system_event" {
		if strings.TrimSpace(step.Content) != "" {
			fmt.Fprintf(sb, "%s\n\n", step.Content)
		}
		return
	}

	if argsJSON, err := json.MarshalIndent(step.Arguments, "", "  "); err == nil && string(argsJSON) != "null" {
		fmt.Fprintf(sb, "<details>\n<summary>Arguments</summary>\n\n```json\n%s\n```\n</details>\n\n", string(argsJSON))
	}
	if step.Result != "" {
		fmt.Fprintf(sb, "<details>\n<summary>Result</summary>\n\n```\n%s\n```\n</details>\n\n", step.Result)
	}
}

// ExecuteTask runs a single step for the project
func (am *AutonomousManager) ExecuteTask(ctx context.Context, projectID string, taskIdx int) error {
	am.mu.Lock()
	if am.isWorking[projectID] {
		am.mu.Unlock()
		return fmt.Errorf("project %s is already undergoing execution", projectID)
	}
	am.isWorking[projectID] = true
	am.mu.Unlock()

	defer func() {
		am.mu.Lock()
		am.isWorking[projectID] = false
		am.mu.Unlock()
	}()

	// Respect the single global background agent slot shared with GoalManager,
	// PlanMonitor, and SleepManager so autonomous tasks never run concurrently
	// with another background loop (which would thrash Ollama/VRAM).
	releaseSlot := sessions.TryAcquireBackgroundSlot()
	if releaseSlot == nil {
		return fmt.Errorf("background slot busy, deferring autonomous task execution")
	}
	defer releaseSlot()

	proj, err := am.LoadProject(projectID)
	if err != nil {
		return err
	}

	if taskIdx < 0 || taskIdx >= len(proj.Todos) {
		return fmt.Errorf("invalid task index %d", taskIdx)
	}

	task := &proj.Todos[taskIdx]
	proj.Status = "in_progress"
	proj.CurrentTask = task.Content
	task.Status = "in_progress"
	task.UpdatedAt = time.Now()
	_ = am.SaveProject(proj)

	model := am.config().OllamaDefaultModel
	if model == "" {
		task.Status = "failed"
		task.Result = "No default model configured in settings. Cannot execute."
		_ = am.SaveProject(proj)
		err := fmt.Errorf("missing Ollama default model")
		am.notifyTaskCompletion(proj, *task, err)
		return err
	}

	log.Printf("[autonomous] Task execution started for project %q: %q", proj.Name, task.Content)

	// Encapsulate workspace: all tool operations inside this project dir!
	projectWorkspaceDir := filepath.Join(am.config().Workspace, projectID)
	_ = os.MkdirAll(projectWorkspaceDir, 0755)

	// Create registry scoped inside this project directory
	registry := tools.NewRegistry(am.config().WebSearchEnabled, projectWorkspaceDir, am.memoryStore, am.client, am.config().OllamaModelEmbed, tools.SearchConfig{
		Providers:    am.config().SearchProviders,
		BraveAPIKey:  am.config().BraveSearchAPIKey,
		TavilyAPIKey: am.config().TavilyAPIKey,
	})
	registry.SetApprovalPolicy(tools.ApprovalPolicyAutonomous)

	// Instantiate iterative agent
	a := NewAgent(am.cfgMgr, am.client, registry)

	// Generate Tick execution system context
	var systemInstructions strings.Builder
	systemInstructions.WriteString(fmt.Sprintf(`## Autonomous Project Mode
You are executing a focused task in an autonomous cycle.
Project ID: %s
Project Name: %s
High-Level Goal: %s

`,
		proj.ID, proj.Name, proj.Goal,
	))

	// Inject context from previously completed tasks in this project so the
	// agent doesn't re-read/re-fetch information that prior tasks already
	// gathered. Each result is truncated to keep the context concise.
	if taskIdx > 0 {
		var priorCtx strings.Builder
		priorCtx.WriteString("## Prior Task Context (completed tasks in this project)\n")
		priorCtx.WriteString("The following tasks were already completed in this project. Use this context to avoid re-reading files or re-fetching information that prior tasks already gathered.\n\n")
		hasPrior := false
		for i := 0; i < taskIdx; i++ {
			pt := proj.Todos[i]
			if pt.Status != "completed" || strings.TrimSpace(pt.Result) == "" {
				continue
			}
			hasPrior = true
			result := pt.Result
			// Truncate long results to keep context manageable.
			if len(result) > 500 {
				result = result[:500] + "... [truncated]"
			}
			fmt.Fprintf(&priorCtx, "### Task %s: %s\nStatus: completed\nResult:\n%s\n\n", pt.ID, pt.Content, result)
		}
		if hasPrior {
			systemInstructions.WriteString(priorCtx.String())
		}
	}

	systemInstructions.WriteString(fmt.Sprintf(`## Current Task to Execute Now
Task ID: %s
Task Description: %s

## Execution Constraints
- Your absolute workspace folder is located at: "%s". Any file read/write/edit operations are automatically mapped to this folder.
- Work step-by-step using tools. Build high-quality, beautiful, robust code files and assets (e.g. index.html, styles.css, app.js).
- Avoid placeholders or incomplete steps.
- When finished, return a clear text response detailing all code files you edited or created, and summarizing the execution result of this task. Do not mention system ticks.`,
		task.ID, task.Content, projectWorkspaceDir,
	))

	messages := []ollama.Message{
		{Role: "system", Content: systemInstructions.String()},
		{Role: "user", Content: fmt.Sprintf("Execute the task: %q", task.Content)},
	}

	// Execution turn with smart retry
	startTime := time.Now()
	var finalHistory []ollama.Message
	var runErr error
	var collector *autonomousStepCollector
	maxRunRetries := 3
	for retry := 0; retry < maxRunRetries; retry++ {
		collector = newAutonomousStepCollector()
		runCtx, runCancel := SubagentContext(ctx, am.config())
		finalHistory, runErr = a.Run(runCtx, model, messages, true, collector)
		runCancel()
		if runErr == nil {
			break
		}
		log.Printf("[autonomous] Error running agent turn for project %s (attempt %d/%d): %v", projectID, retry+1, maxRunRetries, runErr)
		if errors.Is(runErr, context.DeadlineExceeded) {
			break
		}
		if retry < maxRunRetries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
			}
			// Re-create registry and agent to start fresh
			registry = tools.NewRegistry(am.config().WebSearchEnabled, projectWorkspaceDir, am.memoryStore, am.client, am.config().OllamaModelEmbed, tools.SearchConfig{
				Providers:    am.config().SearchProviders,
				BraveAPIKey:  am.config().BraveSearchAPIKey,
				TavilyAPIKey: am.config().TavilyAPIKey,
			})
			registry.SetApprovalPolicy(tools.ApprovalPolicyAutonomous)
			a = NewAgent(am.cfgMgr, am.client, registry)
		}
	}
	elapsed := time.Since(startTime)

	if runErr != nil {
		task.Status = "failed"
		if errors.Is(runErr, context.DeadlineExceeded) {
			task.Result = fmt.Sprintf("Task timed out after %d minutes", am.config().SubagentTimeoutMinutes)
		} else {
			task.Result = fmt.Sprintf("Error running agent turn: %v", runErr)
		}
		_ = am.SaveProject(proj)
		am.notifyTaskCompletion(proj, *task, runErr)
		return runErr
	}

	// Find the final text response from the assistant
	var resultText string
	for i := len(finalHistory) - 1; i >= 0; i-- {
		if finalHistory[i].Role == "assistant" && strings.TrimSpace(finalHistory[i].Content) != "" {
			resultText = finalHistory[i].Content
			break
		}
	}

	// Post-execution verification: independently inspect the workspace to
	// confirm the task was genuinely completed. On failure, mark the task as
	// failed with the identified gaps so it can be retried or reviewed.
	verification := am.verifyTask(ctx, proj, *task, resultText, projectWorkspaceDir)
	if !verification.Success {
		gaps := strings.Join(verification.Gaps, "; ")
		if gaps == "" {
			gaps = "(no specific gaps listed)"
		}
		task.Status = "failed"
		task.Result = fmt.Sprintf("Verification FAILED.\nEvidence: %s\nGaps: %s\nAgent's claimed result:\n%s", verification.Evidence, gaps, resultText)
		task.UpdatedAt = time.Now()
		proj.Status = "pending"
		proj.CurrentTask = ""
		_ = am.SaveProject(proj)
		verifyErr := fmt.Errorf("task verification failed: %s", gaps)
		log.Printf("[autonomous] Task %s verification FAILED for project %q: %s", task.ID, proj.Name, gaps)
		am.notifyTaskCompletion(proj, *task, verifyErr)
		return verifyErr
	}
	log.Printf("[autonomous] Task %s verification passed for project %q", task.ID, proj.Name)

	task.Status = "completed"
	task.Result = resultText
	task.UpdatedAt = time.Now()

	// Recalculate project overall status
	allDone := true
	for _, t := range proj.Todos {
		if t.Status != "completed" {
			allDone = false
			break
		}
	}
	if allDone {
		proj.Status = "completed"
		proj.CurrentTask = ""
	} else {
		proj.Status = "pending" // Wait for next tick
		proj.CurrentTask = ""
	}
	_ = am.SaveProject(proj)

	// Save detailed execution tick markdown log inside logs directory
	logsDir := filepath.Join(projectWorkspaceDir, "logs")
	_ = os.MkdirAll(logsDir, 0755)

	logFilename := fmt.Sprintf("heartbeat_%s_%s.md", task.ID, time.Now().Format("20060102_150405"))
	logPath := filepath.Join(logsDir, logFilename)

	var logContent strings.Builder
	fmt.Fprintf(&logContent, "# Heartbeat Execution Log: %s\n\n", task.ID)
	fmt.Fprintf(&logContent, "- **Project:** %s (%s)\n", proj.Name, proj.ID)
	fmt.Fprintf(&logContent, "- **Goal:** %s\n", proj.Goal)
	fmt.Fprintf(&logContent, "- **Task:** %s\n", task.Content)
	fmt.Fprintf(&logContent, "- **Execution Time:** %s\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&logContent, "- **Duration:** %v\n", elapsed)
	fmt.Fprintf(&logContent, "- **Status:** %s\n\n", task.Status)
	fmt.Fprintf(&logContent, "## Execution Result Summary\n\n%s\n\n", resultText)

	// Structured tool execution steps for debugging (name, source, status, timing).
	fmt.Fprintf(&logContent, "## Tool Execution Steps\n\n")
	steps := collector.snapshot()
	if len(steps) == 0 {
		fmt.Fprintf(&logContent, "_(no tool steps recorded)_\n\n")
	} else {
		for i, step := range steps {
			writeAutonomousStep(&logContent, i+1, step)
		}
	}

	fmt.Fprintf(&logContent, "--- \n## Raw Conversation Turns\n\n")

	for _, msg := range finalHistory {
		if msg.Role == "system" {
			continue // Skip long instructions for readability
		}
		fmt.Fprintf(&logContent, "### Role: `%s` \n", msg.Role)
		if msg.Thinking != "" {
			fmt.Fprintf(&logContent, "<details>\n<summary>Thinking Process</summary>\n\n%s\n</details>\n\n", msg.Thinking)
		}
		if msg.Content != "" {
			fmt.Fprintf(&logContent, "%s\n\n", msg.Content)
		}
		if len(msg.ToolCalls) > 0 {
			fmt.Fprintf(&logContent, "#### Tool Calls:\n")
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&logContent, "- Call `%s` with args: `%s`\n", tc.Function.Name, string(tc.Function.Arguments))
			}
			fmt.Fprintf(&logContent, "\n")
		}
	}

	_ = os.WriteFile(logPath, []byte(logContent.String()), 0644)
	log.Printf("[autonomous] Task execution completed successfully for project %q: %s", proj.Name, task.ID)

	am.notifyTaskCompletion(proj, *task, nil)

	return nil
}

// DeleteProject deletes the project folder inside workspace
func (am *AutonomousManager) DeleteProject(id string) error {
	projDir := filepath.Join(am.config().Workspace, id)
	return os.RemoveAll(projDir)
}

// GetProjectLogs returns all generated tick execution markdown log filenames for a project
func (am *AutonomousManager) GetProjectLogs(id string) ([]string, error) {
	logsDir := filepath.Join(am.config().Workspace, id, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		return []string{}, nil
	}
	files, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, err
	}
	var logs []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
			logs = append(logs, f.Name())
		}
	}
	return logs, nil
}

// verificationSchema enforces structured output from the verification agent.
var verificationSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"success":  map[string]any{"type": "boolean"},
		"evidence": map[string]any{"type": "string"},
		"gaps": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required": []string{"success", "evidence"},
}

// verificationResponse is the parsed result of the verification agent turn.
type verificationResponse struct {
	Success  bool     `json:"success"`
	Evidence string   `json:"evidence"`
	Gaps     []string `json:"gaps"`
}

// verifyTask runs a short, focused agent turn that independently inspects the
// project workspace to confirm the task was actually completed. It uses the
// subagent model when available (cheaper), falling back to the main model.
// Returns the parsed verification response, or a permissive default
// (success=true) if verification is disabled or fails to run, so that
// verification issues never block tasks that would otherwise complete.
func (am *AutonomousManager) verifyTask(ctx context.Context, proj Project, task ProjectTodo, resultText, projectWorkspaceDir string) verificationResponse {
	if !am.config().AutonomousVerificationEnabled {
		return verificationResponse{Success: true, Evidence: "verification disabled"}
	}

	verifyModel := am.config().OllamaModelSubagent
	if strings.TrimSpace(verifyModel) == "" {
		verifyModel = am.config().OllamaDefaultModel
	}
	if verifyModel == "" {
		return verificationResponse{Success: true, Evidence: "no model available for verification"}
	}

	// Scoped registry so the verifier can read files in the project dir.
	registry := tools.NewRegistry(am.config().WebSearchEnabled, projectWorkspaceDir, am.memoryStore, am.client, am.config().OllamaModelEmbed, tools.SearchConfig{
		Providers:    am.config().SearchProviders,
		BraveAPIKey:  am.config().BraveSearchAPIKey,
		TavilyAPIKey: am.config().TavilyAPIKey,
	})
	registry.SetApprovalPolicy(tools.ApprovalPolicyAutonomous)
	a := NewAgent(am.cfgMgr, am.client, registry)

	prompt := fmt.Sprintf(`You are a strict code reviewer verifying whether an autonomous task was completed correctly.

Project: %s
Goal: %s
Task: %s
Workspace folder: %s
Agent's claimed result:
%s

Use list_files and read_file to inspect the workspace folder. Verify that the files the agent claims to have created or modified actually exist and contain meaningful, non-placeholder content. Check for obvious errors, empty stubs, or incomplete implementations.

Respond with a JSON object conforming to the schema:
- success: true only if the task objective is genuinely fulfilled.
- evidence: concrete description of what you found (file names, key content snippets).
- gaps: list of specific missing or broken items (empty array if none).`,
		proj.Name, proj.Goal, task.Content, projectWorkspaceDir, resultText)

	messages := []ollama.Message{
		{Role: "system", Content: "You are a verification agent. Inspect files and report structured JSON only."},
		{Role: "user", Content: prompt},
	}

	verifyCtx, cancel := SubagentContext(ctx, am.config())
	defer cancel()

	finalHistory, err := a.Run(verifyCtx, verifyModel, messages, false, &dummyStreamHandler{})
	if err != nil {
		log.Printf("[autonomous] Verification run failed for task %s: %v (accepting task as completed)", task.ID, err)
		return verificationResponse{Success: true, Evidence: fmt.Sprintf("verification run failed: %v", err)}
	}

	// Extract the last assistant message with content.
	var raw string
	for i := len(finalHistory) - 1; i >= 0; i-- {
		if finalHistory[i].Role == "assistant" && strings.TrimSpace(finalHistory[i].Content) != "" {
			raw = finalHistory[i].Content
			break
		}
	}
	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if present.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var vr verificationResponse
	if err := json.Unmarshal([]byte(raw), &vr); err != nil {
		log.Printf("[autonomous] Verification response not valid JSON for task %s: %v (raw: %s). Accepting task as completed.", task.ID, err, truncate(raw, 200))
		return verificationResponse{Success: true, Evidence: fmt.Sprintf("verification response unparseable: %v", err)}
	}
	return vr
}
