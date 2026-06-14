package agent

import (
	"context"
	"encoding/json"
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

// OnTaskCompletion is a global callback triggered when an autonomous task completes or fails
var OnTaskCompletion TaskNotificationFunc

// AutonomousManager manages background tickers and executions of workspace projects
type AutonomousManager struct {
	mu          sync.RWMutex
	cfg         config.Config
	client      *ollama.Client
	memoryStore *memory.Store
	isWorking   map[string]bool
	cancelFunc  context.CancelFunc
	tickerDone  chan struct{}
	interval    time.Duration
}

// NewAutonomousManager creates a new instance of AutonomousManager
func NewAutonomousManager(cfg config.Config, client *ollama.Client, memoryStore *memory.Store) *AutonomousManager {
	return &AutonomousManager{
		cfg:         cfg,
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

// ListProjects scans the workspace root for folders containing "project.json"
func (am *AutonomousManager) ListProjects() ([]Project, error) {
	workspaceRoot := am.cfg.Workspace
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
	projPath := filepath.Join(am.cfg.Workspace, id, "project.json")
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
	projDir := filepath.Join(am.cfg.Workspace, proj.ID)
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
	projDir := filepath.Join(am.cfg.Workspace, id)
	if _, err := os.Stat(projDir); err == nil {
		id = fmt.Sprintf("%s-%d", id, time.Now().Unix()%1000)
		projDir = filepath.Join(am.cfg.Workspace, id)
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

// generateInitialTodos calls Ollama to design a checklist of tasks
func (am *AutonomousManager) generateInitialTodos(ctx context.Context, name, goal string) ([]ProjectTodo, error) {
	model := am.cfg.OllamaDefaultModel
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

Return ONLY a raw JSON array of task objects, formatted exactly as:
[
  {"id": "task-1", "content": "detailed task description..."},
  {"id": "task-2", "content": "detailed task description..."}
]
Do NOT write any conversational text, markdown formatting blocks, or packaging. Return only valid JSON.`, name, goal)

	req := ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: prompt},
		},
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

	type rawTodo struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}

	var rawList []rawTodo
	if err := json.Unmarshal([]byte(rawText), &rawList); err != nil {
		// Log response for debugging
		log.Printf("[autonomous] Error parsing JSON todos: %v. Raw text was: %s", err, rawText)
		return nil, err
	}

	var list []ProjectTodo
	for _, rt := range rawList {
		list = append(list, ProjectTodo{
			ID:        rt.ID,
			Content:   rt.Content,
			Status:    "pending",
			UpdatedAt: time.Now(),
		})
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

func (d *dummyStreamHandler) OnThinking(delta string)                 {}
func (d *dummyStreamHandler) OnContent(delta string)                  {}
func (d *dummyStreamHandler) OnToolCall(call ollama.ToolCall)         {}
func (d *dummyStreamHandler) OnToolStart(name string, args any)       {}
func (d *dummyStreamHandler) OnToolResult(name string, result string) {}
func (d *dummyStreamHandler) OnMediaPreProcessing(content string)     {}
func (d *dummyStreamHandler) OnDone(resp ollama.ChatResponse)         {}

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

	model := am.cfg.OllamaDefaultModel
	if model == "" {
		task.Status = "failed"
		task.Result = "No default model configured in settings. Cannot execute."
		_ = am.SaveProject(proj)
		err := fmt.Errorf("missing Ollama default model")
		if OnTaskCompletion != nil {
			OnTaskCompletion(proj, *task, err)
		}
		return err
	}

	log.Printf("[autonomous] Task execution started for project %q: %q", proj.Name, task.Content)

	// Encapsulate workspace: all tool operations inside this project dir!
	projectWorkspaceDir := filepath.Join(am.cfg.Workspace, projectID)
	_ = os.MkdirAll(projectWorkspaceDir, 0755)

	// Create registry scoped inside this project directory
	registry := tools.NewRegistry(am.cfg.WebSearchEnabled, projectWorkspaceDir, am.memoryStore, am.client, am.cfg.OllamaModelEmbed, tools.SearchConfig{
		Providers:   am.cfg.SearchProviders,
		BraveAPIKey: am.cfg.BraveSearchAPIKey,
	})

	// Instantiate iterative agent
	a := NewAgent(am.cfg, am.client, registry)

	// Generate Tick execution system context
	var systemInstructions strings.Builder
	systemInstructions.WriteString(fmt.Sprintf(`## Autonomous Project Mode
You are executing a focused task in an autonomous cycle.
Project ID: %s
Project Name: %s
High-Level Goal: %s

## Current Task to Execute Now
Task ID: %s
Task Description: %s

## Execution Constraints
- Your absolute workspace folder is located at: "%s". Any file read/write/edit operations are automatically mapped to this folder.
- Work step-by-step using tools. Build high-quality, beautiful, robust code files and assets (e.g. index.html, styles.css, app.js).
- Avoid placeholders or incomplete steps.
- When finished, return a clear text response detailing all code files you edited or created, and summarizing the execution result of this task. Do not mention system ticks.`,
		proj.ID, proj.Name, proj.Goal, task.ID, task.Content, projectWorkspaceDir,
	))

	messages := []ollama.Message{
		{Role: "system", Content: systemInstructions.String()},
		{Role: "user", Content: fmt.Sprintf("Execute the task: %q", task.Content)},
	}

	// Execution turn with smart retry
	startTime := time.Now()
	var finalHistory []ollama.Message
	var runErr error
	maxRunRetries := 3
	for retry := 0; retry < maxRunRetries; retry++ {
		finalHistory, runErr = a.Run(ctx, model, messages, true, &dummyStreamHandler{})
		if runErr == nil {
			break
		}
		log.Printf("[autonomous] Error running agent turn for project %s (attempt %d/%d): %v", projectID, retry+1, maxRunRetries, runErr)
		if retry < maxRunRetries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
			}
			// Re-create registry and agent to start fresh
			registry = tools.NewRegistry(am.cfg.WebSearchEnabled, projectWorkspaceDir, am.memoryStore, am.client, am.cfg.OllamaModelEmbed, tools.SearchConfig{
				Providers:   am.cfg.SearchProviders,
				BraveAPIKey: am.cfg.BraveSearchAPIKey,
			})
			a = NewAgent(am.cfg, am.client, registry)
		}
	}
	elapsed := time.Since(startTime)

	if runErr != nil {
		task.Status = "failed"
		task.Result = fmt.Sprintf("Error running agent turn: %v", runErr)
		_ = am.SaveProject(proj)
		if OnTaskCompletion != nil {
			OnTaskCompletion(proj, *task, runErr)
		}
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

	if OnTaskCompletion != nil {
		OnTaskCompletion(proj, *task, nil)
	}

	return nil
}

// DeleteProject deletes the project folder inside workspace
func (am *AutonomousManager) DeleteProject(id string) error {
	projDir := filepath.Join(am.cfg.Workspace, id)
	return os.RemoveAll(projDir)
}

// GetProjectLogs returns all generated tick execution markdown log filenames for a project
func (am *AutonomousManager) GetProjectLogs(id string) ([]string, error) {
	logsDir := filepath.Join(am.cfg.Workspace, id, "logs")
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
