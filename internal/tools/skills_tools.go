package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/skills"
)

// cleanSkillName sanitizes a input name into a safe directory name.
func cleanSkillName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// formatInstructions guarantees instructions are checklist items.
func formatInstructions(inst string) string {
	lines := strings.Split(inst, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- [ ] ") || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			out = append(out, trimmed)
		} else {
			out = append(out, "- [ ] "+trimmed)
		}
	}
	return strings.Join(out, "\n")
}

// ListSkills returns a summary of all loaded skills.
func ListSkills(skillsPath string) (string, error) {
	cat, err := skills.NewCatalog(skillsPath)
	if err != nil {
		return "", err
	}
	metaList, err := cat.List()
	if err != nil {
		return "", err
	}
	if len(metaList) == 0 {
		return "No skills found.", nil
	}
	var sb strings.Builder
	for _, m := range metaList {
		sb.WriteString(fmt.Sprintf("- Name: %s\n  Description: %s\n  Homepage: %s\n  Path: %s\n\n", m.Name, m.Description, m.Homepage, m.SkillFile))
	}
	return sb.String(), nil
}

// GetSkill retrieves the raw markdown of a skill.
func GetSkill(skillsPath, name string) (string, error) {
	safeName := cleanSkillName(name)
	if safeName == "" {
		return "", fmt.Errorf("invalid skill name")
	}
	path := filepath.Join(skillsPath, safeName, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("skill not found: %w", err)
	}
	return string(data), nil
}

// CreateSkill creates a new skill directory and SKILL.md.
func CreateSkill(skillsPath, name, description, homepage, instructions string) error {
	safeName := cleanSkillName(name)
	if safeName == "" {
		return fmt.Errorf("invalid skill name")
	}
	dir := filepath.Join(skillsPath, safeName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create skill directory: %w", err)
	}
	path := filepath.Join(dir, "SKILL.md")

	formattedInst := formatInstructions(instructions)

	content := fmt.Sprintf(`---
name: %s
description: %s
homepage: %s
---

## Description
%s

## Instructions
%s
`, safeName, description, homepage, description, formattedInst)

	if _, err := skills.ParseSkillMarkdown(content); err != nil {
		return fmt.Errorf("skill validation failed: %w", err)
	}

	return os.WriteFile(path, []byte(content), 0644)
}

// EditSkill edits an existing skill, merging new fields with current values.
func EditSkill(skillsPath, name, description, homepage, instructions string) error {
	safeName := cleanSkillName(name)
	if safeName == "" {
		return fmt.Errorf("invalid skill name")
	}
	path := filepath.Join(skillsPath, safeName, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("skill not found: %w", err)
	}

	content := string(data)
	var currentDesc, currentHome, currentInstructions string

	parts := strings.SplitN(content, "---", 3)
	if len(parts) >= 3 {
		fmLines := strings.Split(parts[1], "\n")
		for _, line := range fmLines {
			line = strings.TrimSpace(line)
			colon := strings.Index(line, ":")
			if colon <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:colon])
			val := strings.TrimSpace(line[colon+1:])
			if key == "description" {
				currentDesc = val
			} else if key == "homepage" {
				currentHome = val
			}
		}

		body := parts[2]
		instIdx := strings.Index(strings.ToLower(body), "## instructions")
		if instIdx == -1 {
			instIdx = strings.Index(strings.ToLower(body), "## steps")
		}
		if instIdx != -1 {
			// Find start of instructions content after headers
			lines := strings.Split(body[instIdx:], "\n")
			var instLines []string
			for _, line := range lines[1:] {
				instLines = append(instLines, line)
			}
			currentInstructions = strings.TrimSpace(strings.Join(instLines, "\n"))
		} else {
			currentInstructions = strings.TrimSpace(body)
		}
	}

	finalDesc := description
	if finalDesc == "" {
		finalDesc = currentDesc
	}
	finalHome := homepage
	if finalHome == "" {
		finalHome = currentHome
	}
	finalInst := instructions
	if finalInst == "" {
		finalInst = currentInstructions
	}

	return CreateSkill(skillsPath, safeName, finalDesc, finalHome, finalInst)
}

// DeleteSkill deletes a skill folder.
func DeleteSkill(skillsPath, name string) error {
	safeName := cleanSkillName(name)
	if safeName == "" {
		return fmt.Errorf("invalid skill name")
	}
	dir := filepath.Join(skillsPath, safeName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("skill directory does not exist")
	}
	return os.RemoveAll(dir)
}
