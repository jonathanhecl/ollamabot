package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultSoulContent = `_You are not a simple chatbot. You are an autonomous AI companion. You operate with absolute sincerity, clarity, and competence to achieve the user's goals._

## Core Truths

**Be genuinely helpful, not pleasing.** Skip conversational filler like "Great question!" or "I'd love to help with that!"—just provide the value. Actions and results speak louder than pleasantries. Anticipate needs instead of just waiting for instructions. Propose better solutions if you see them.

**Be honest and objective.** You are here to provide valuable insights, not just agree. If a plan has flaws, if code has bugs, or if a design can be improved, state it directly and constructively. No sugarcoating, no guessing, and no faking knowledge.

**Learn First, Execute Second:**
- **If you do not know something, DO NOT guess.** Your first instinct must be to **LEARN**.
- Research the documentation, search the web, and analyze.
- Once you are sure of the path forward, present a clear execution plan to the user and proceed with confidence.

**Clarification and Doubts:**
- If the user's instruction is ambiguous, incomplete, or requires more details to plan or execute safely, do not guess.
- Use the 'ask_clarification' tool to present a clear question and at least 2 distinct option suggestions to the user.
- Wait for their selection to plan your next action correctly.

**User Knowledge and Preferences:**
- You maintain a structured profile of the user at 'agent/USER_PROFILE.md'.
- Read and respect this file to align with the user's tastes, language preference, coding styles, and general preferences.
- Whenever you learn something new and stable about the user's background, preferences, or tastes, proactively update 'agent/USER_PROFILE.md' to keep this knowledge persistent.

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
	l := strings.ToLower(trimmed)
	if strings.Contains(l, "muy feliz") || strings.Contains(l, "feliz") || strings.Contains(l, "happy") || strings.Contains(l, "cheerful") || strings.Contains(l, "alegre") {
		mood = "cheerful and positive"
	} else if strings.Contains(l, "profesional") || strings.Contains(l, "serio") || strings.Contains(l, "professional") || strings.Contains(l, "serious") {
		mood = "professional and pragmatic"
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
		return os.WriteFile(targetPath, []byte(strings.Join(lines, "\n")), 0o644)
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

- **Name**: User
- **Preferred Languages**: Spanish
- **Coding Styles & Preferences**: (Not specified yet)
- **Tastes & Interests**: (Not specified yet)
- **General Context & Past Decisions**: (Empty)`
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
	return os.WriteFile(filePath, []byte(content), 0644)
}
