package skills

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Skill represents a parsed skill document with explicit execution steps.
type Skill struct {
	Name        string
	Description string
	Steps       []Step
}

// Step represents one step from the skill instructions.
type Step struct {
	Index       int
	Instruction string
}

// LoadSkillFromFile reads and parses a SKILL markdown file from disk.
func LoadSkillFromFile(path string) (Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("read skill file: %w", err)
	}
	return ParseSkillMarkdown(string(content))
}

// ParseSkillMarkdown extracts skill name, description, and step list from markdown.
func ParseSkillMarkdown(markdown string) (Skill, error) {
	trimmed := strings.TrimSpace(markdown)
	var frontmatter map[string]string
	var body string
	if strings.HasPrefix(trimmed, "---") {
		var err error
		frontmatter, body, err = parseFrontmatter(markdown)
		if err == nil {
			markdown = body
		}
	}

	lines := strings.Split(markdown, "\n")

	var skill Skill
	if frontmatter != nil {
		skill.Name = strings.TrimSpace(trimQuotes(frontmatter["name"]))
		skill.Description = strings.TrimSpace(trimQuotes(frontmatter["description"]))
	}

	var descLines []string
	inDescription := false
	inSteps := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if inDescription {
				descLines = append(descLines, "")
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "# "):
			if skill.Name == "" {
				skill.Name = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
		case strings.EqualFold(line, "## Description"):
			inDescription = true
			inSteps = false
		case strings.EqualFold(line, "## Steps") || strings.EqualFold(line, "## Instructions"):
			inDescription = false
			inSteps = true
		case strings.HasPrefix(line, "## "):
			inDescription = false
			inSteps = false
		default:
			if inDescription {
				descLines = append(descLines, line)
				continue
			}
			if inSteps {
				if step, ok := parseStepLine(line, len(skill.Steps)+1); ok {
					skill.Steps = append(skill.Steps, step)
				}
			}
		}
	}

	if len(descLines) > 0 {
		skill.Description = strings.TrimSpace(strings.Join(descLines, "\n"))
	}
	if skill.Name == "" {
		return Skill{}, errors.New("skill name not found")
	}
	if len(skill.Steps) == 0 {
		return Skill{}, errors.New("skill has no steps")
	}
	return skill, nil
}

func parseStepLine(line string, defaultIndex int) (Step, bool) {
	if strings.HasPrefix(line, "- [ ] ") {
		return Step{
			Index:       defaultIndex,
			Instruction: strings.TrimSpace(strings.TrimPrefix(line, "- [ ] ")),
		}, true
	}
	if strings.HasPrefix(line, "- ") {
		return Step{
			Index:       defaultIndex,
			Instruction: strings.TrimSpace(strings.TrimPrefix(line, "- ")),
		}, true
	}

	dotIndex := strings.Index(line, ". ")
	if dotIndex <= 0 {
		return Step{}, false
	}
	idx, err := strconv.Atoi(strings.TrimSpace(line[:dotIndex]))
	if err != nil {
		return Step{}, false
	}
	instruction := strings.TrimSpace(line[dotIndex+2:])
	if instruction == "" {
		return Step{}, false
	}
	return Step{
		Index:       idx,
		Instruction: instruction,
	}, true
}
