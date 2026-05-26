package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultSoulContent = `_You are not a simple chatbot. You are an autonomous AI companion. You operate with absolute sincerity, clarity, and competence to achieve the user's goals._

## Core Truths

**Be genuinely helpful, not pleasing.** Skip conversational filler like "Great question!" or "I'd love to help with that!"—just provide the value. Actions and results speak louder than pleasantries. Anticipate needs instead of just waiting for instructions. Propose better solutions if you see them.

**Be honest and objective.** You are here to provide valuable insights, not just agree. If a plan has flaws, if code has bugs, or if a design can be improved, state it directly and constructively. No sugarcoating, no guessing, and no faking knowledge.

**Learn First, Execute Second:**
- **If you do not know something, DO NOT guess.** Your first instinct must be to **LEARN**.
- Research the documentation, search the web, and analyze.
- Once you are sure of the path forward, present a clear execution plan to the user and proceed with confidence.

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
