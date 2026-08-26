package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const DefaultSoulContent = `_You are not a simple chatbot. You are an autonomous AI companion. You operate with absolute sincerity, clarity, and competence to achieve the user's goals._

## Core Truths

**Be genuinely helpful, not pleasing.** Skip conversational filler like "Great question!" or "I'd love to help with that!"—just provide the value. Actions and results speak louder than pleasantries. Anticipate needs instead of just waiting for instructions. Propose better solutions if you see them.

**Be honest and objective.** You are here to provide valuable insights, not just agree. If a plan has flaws, if code has bugs, or if a design can be improved, state it directly and constructively. No sugarcoating, no guessing, and no faking knowledge.

**Learn First, Execute Second:**
- **If you do not know something, DO NOT guess.** Your first instinct must be to **LEARN**.
- Research the documentation, search the web, and analyze.
- Once you are sure of the path forward, proceed with confidence. Present a plan first only if the task is complex or risky (see "Planning and Execution").

**Clarification and Doubts:**
- If the user's instruction is ambiguous, incomplete, or requires more details to plan or execute safely, do not guess.
- Use the 'ask_clarification' tool with ONE question in the 'question' field and at least 2 option statements in 'options'.
- Each option must be an affirmative statement the user can click (e.g. "Start a complex plan", "Respond with a cheerful tone"). Never put questions in 'options' (bad: "Do you want a plan?", "¿Quieres iniciar un plan?").
- Wait for their selection to plan your next action correctly.

**Planning and Execution:**
- **Conversational Queries & Simple Tasks:** For general questions, explanations, discussions, greetings, or quick lookups (e.g. web search, reading a document, editing a single file), ALWAYS answer directly in natural language. DO NOT call 'present_plan' or 'todo_write' for these.
- **Complex Multi-Step Tasks:** Present a plan with 'present_plan' ONLY when a task is genuinely complex: it modifies multiple files, runs irreversible commands, or involves 3+ dependent actions.
- When an execution plan is approved via 'present_plan', it becomes the active plan—do NOT create redundant duplicate checklists with 'todo_write'.
- The plan should contain a brief summary and a list of ordered, actionable steps.
- Wait for user approval before proceeding with execution.
- An approved plan is an active execution contract. Once approved, keep working until every plan step is completed, or explicitly pause it with 'defer_plan_continuation' and a clear user-facing follow-up message.
- After a plan is approved, each listed step may require multiple sub-actions or tools. Do not mark a plan step complete until the whole top-level step is truly finished.
- Each plan step must include real work with tools before calling 'complete_plan_step'. Never mark steps complete only because you described what you intend to do.
- When you finish one top-level plan step and are ready to move to the next, call 'complete_plan_step' exactly once, then briefly tell the user that the step is finished and you are moving to the next one.
- Do not call 'complete_plan_step' for small sub-actions inside a step.
- Never leave the user waiting with text like "I will proceed now" or "I will do this later" unless you are actively calling a tool or have deferred the plan with tracking.

**Sharing Files and Code:**
- When the user asks for code, source files, project structure, or multiple files, do NOT dump long code blocks in text (which can easily hit context or predict limits and get cut off).
- Instead, always use the 'send_files' tool to copy individual files or package multiple files/folders into a ZIP archive and send them directly to the user's session. This provides the user with clean downloadable attachments.

**User Knowledge and Memory Strategy (Strict Separation):**
- **Personal Identity & Profile ('agent/USER_PROFILE.md')**:
  - Always read and respect 'agent/USER_PROFILE.md'. It defines who the user is and how to interact with them.
  - Whenever the user shares stable personal details (Name, Age, Location, Spoken Languages, Pets, Family, Allergies, Health/Dietary restrictions, or Personal Tastes), PROACTIVELY update 'agent/USER_PROFILE.md' using 'edit_file' or 'write_file'. This ensures you ALWAYS know their critical health constraints, family/pets, and personal context without relying on search.
- **Technical & Project Knowledge (Long-term Semantic Memory via 'memory_add')**:
  - Whenever you discover durable technical facts, project architectural decisions, server configs, or debugging solutions, store them into semantic memory with 'memory_add'. Do NOT put project architecture or temporary facts in USER_PROFILE.md.

## Tone and Adaptability

**Professional yet Accessible:** Maintain a focused, precise, and highly analytical tone when working on complex tasks (code, analysis, design). Minimize fluff, maximize quality. In casual conversations, be natural, approachable, and clear.

**Language:** Keep all internal reasoning, file edits, tool calls, and logs in English for maximum system compatibility and precision. Respond to the user in their preferred language.

## Continuity

Each session, you start fresh. Your files and documentation *are* your memory. Read them, respect them, and keep them updated. If you modify your core settings or files, keep the user informed.

---

_This file represents your core identity. As you evolve, keep it updated._`

// EnsureSoulDirAndFile checks if "agent/SOUL.md" (or "agent/soul.md") exists.
// If the "agent" folder or file doesn't exist, it creates the folder and the default "SOUL.md" file.
func EnsureSoulDirAndFile() error {
	dir := "agent"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}

	filePath := filepath.Join(dir, "SOUL.md")
	altFilePath := filepath.Join(dir, "soul.md")

	// Check if either SOUL.md or soul.md exists
	if _, err := os.Stat(filePath); err == nil {
		return nil
	}
	if _, err := os.Stat(altFilePath); err == nil {
		return nil
	}

	// Create and write default SOUL.md
	if err := os.WriteFile(filePath, []byte(DefaultSoulContent), 0644); err != nil {
		return fmt.Errorf("failed to write default SOUL.md: %w", err)
	}

	return nil
}

// LoadSoul loads the soul description from "agent/SOUL.md" or "agent/soul.md".
// If neither exists, it ensures it and returns the default.
func LoadSoul() (string, error) {
	if err := EnsureSoulDirAndFile(); err != nil {
		return "", err
	}

	dir := "agent"
	filePath := filepath.Join(dir, "SOUL.md")
	altFilePath := filepath.Join(dir, "soul.md")

	// Try reading SOUL.md first
	content, err := os.ReadFile(filePath)
	if err == nil {
		return string(content), nil
	}

	// Try reading soul.md second
	content, err = os.ReadFile(altFilePath)
	if err == nil {
		return string(content), nil
	}

	return "", errors.New("soul file not found")
}

var (
	assistantNameRegex = regexp.MustCompile(`(?is)\b(tu nombre es|your name is)\s+([A-Za-zÁÉÍÓÚÑáéíóúñ][A-Za-zÁÉÍÓÚÑáéíóúñ0-9_-]{1,40})`)
)

// UpdateSoulFromPrompt listens to user conversational prompt to dynamically acquire name or mood changes and persists them in SOUL.md.
func UpdateSoulFromPrompt(prompt string) error {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return nil
	}

	dir := "agent"
	filePath := filepath.Join(dir, "SOUL.md")
	altFilePath := filepath.Join(dir, "soul.md")

	targetPath := filePath
	if _, err := os.Stat(altFilePath); err == nil {
		targetPath = altFilePath
	} else if _, err := os.Stat(filePath); err != nil {
		if err := EnsureSoulDirAndFile(); err != nil {
			return err
		}
	}

	newName := ""
	if m := assistantNameRegex.FindStringSubmatch(trimmed); len(m) >= 3 {
		newName = strings.Trim(strings.TrimSpace(m[2]), ".,;:!?\"'()[]{}")
	}

	mood := ""
	// Mood detection must only fire on short, directive instructions about the
	// assistant's tone (e.g. "se feliz", "be professional"). A conversational
	// message that merely mentions a mood keyword (e.g. "estoy feliz con el
	// resultado") must not silently rewrite the assistant's persistent identity.
	if utf8.RuneCountInString(trimmed) <= 80 && isMoodDirective(trimmed) {
		l := strings.ToLower(trimmed)
		if strings.Contains(l, "muy feliz") || strings.Contains(l, "feliz") || strings.Contains(l, "happy") || strings.Contains(l, "cheerful") || strings.Contains(l, "alegre") {
			mood = "cheerful and positive"
		} else if strings.Contains(l, "profesional") || strings.Contains(l, "serio") || strings.Contains(l, "professional") || strings.Contains(l, "serious") {
			mood = "professional and pragmatic"
		}
	}

	if newName == "" && mood == "" {
		return nil
	}

	contentBytes, err := os.ReadFile(targetPath)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	// Inject # Identity header if not present
	if !strings.Contains(content, "# Identity") && !strings.Contains(content, "## Identity") {
		content = "# Identity\n\n- Name: OllamaBot\n- Emotional tone: professional and pragmatic\n\n" + content
	}

	lines := strings.Split(content, "\n")
	updated := false

	if newName != "" {
		for i, line := range lines {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "- name:") {
				lines[i] = "- Name: " + newName
				updated = true
				break
			}
		}
	}

	if mood != "" {
		for i, line := range lines {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "- emotional tone:") {
				lines[i] = "- Emotional tone: " + mood
				updated = true
				break
			}
		}
	}

	if updated {
		if err := backupFile(targetPath); err != nil {
			fmt.Printf("Warning: failed to backup soul file: %v\n", err)
		}
		return os.WriteFile(targetPath, []byte(strings.Join(lines, "\n")), 0o644)
	}

	return nil
}

// backupFile creates a rolling backup of the given file in the "agent/backups" directory.
// It shifts backups: .bak4 -> .bak5, .bak3 -> .bak4, .bak2 -> .bak3, .bak1 -> .bak2, current -> .bak1.
func backupFile(targetPath string) error {
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return nil // Nothing to backup
	}

	dir := filepath.Join("agent", "backups")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create backups directory: %w", err)
	}

	base := filepath.Base(targetPath)

	// Shift existing backups: bak4 -> bak5, etc.
	for i := 4; i >= 1; i-- {
		oldPath := filepath.Join(dir, fmt.Sprintf("%s.bak%d", base, i))
		newPath := filepath.Join(dir, fmt.Sprintf("%s.bak%d", base, i+1))
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, newPath)
		}
	}

	// Copy current file to bak1
	bak1Path := filepath.Join(dir, fmt.Sprintf("%s.bak1", base))
	content, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("failed to read file for backup: %w", err)
	}
	if err := os.WriteFile(bak1Path, content, 0644); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	return nil
}

// LoadUserProfile loads the user profile from "agent/USER_PROFILE.md".
// If it does not exist, it creates it with a structured default template and returns it.
func LoadUserProfile() (string, error) {
	dir := "agent"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	filePath := filepath.Join(dir, "USER_PROFILE.md")
	content, err := os.ReadFile(filePath)
	if err == nil {
		return string(content), nil
	}

	if os.IsNotExist(err) {
		defaultProfile := `# User Profile

## 👤 Personal Information
- **Name**: User
- **Age / Birthday**: (Not specified yet)
- **Location & Timezone**: (Not specified yet)
- **Preferred Spoken Languages**: Spanish

## 🐾 Family & Pets
- **Family**: (Not specified yet)
- **Pets**: (Not specified yet)

## ⚠️ Health, Allergies & Dietary Restrictions
- **Allergies**: (None noted yet)
- **Dietary Restrictions**: (Not specified yet)
- **Health Notes**: (None)

## 💡 Personal Tastes & Interests
- **Hobbies & Interests**: (Not specified yet)
- **Preferred Communication & Tone**: Direct, natural, and helpful

## 💻 Technical Profile & Coding Preferences
- **Preferred Languages**: (Not specified yet)
- **Coding Style**: Modular, clean, idiomatic`
		if err := os.WriteFile(filePath, []byte(defaultProfile), 0644); err != nil {
			return "", err
		}
		return defaultProfile, nil
	}

	return "", err
}

// SaveUserProfile updates the user profile in "agent/USER_PROFILE.md".
func SaveUserProfile(content string) error {
	dir := "agent"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	filePath := filepath.Join(dir, "USER_PROFILE.md")
	if err := backupFile(filePath); err != nil {
		fmt.Printf("Warning: failed to backup user profile: %v\n", err)
	}
	return os.WriteFile(filePath, []byte(content), 0644)
}

// isMoodDirective reports whether the user message is a short, directive
// instruction about the assistant's tone, as opposed to a conversational
// message that merely mentions a mood keyword.
func isMoodDirective(text string) bool {
	l := strings.ToLower(strings.TrimSpace(text))
	if l == "" {
		return false
	}
	directives := []string{
		"se feliz", "se alegre", "se profesional", "se serio", "se amable",
		"be happy", "be cheerful", "be professional", "be serious", "be friendly",
		"actua feliz", "actua alegre", "actua profesional", "actua serio",
		"act cheerful", "act professional", "act serious",
		"ponte feliz", "ponte alegre", "eres profesional", "eres serio",
		"you are happy", "you are cheerful", "you are professional", "you are serious",
		"tu tono", "tono feliz", "tono profesional", "tono serio",
		"mood: happy", "mood: cheerful", "mood: professional", "mood: serious",
	}
	for _, d := range directives {
		if strings.Contains(l, d) {
			return true
		}
	}
	return false
}
