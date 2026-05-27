package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Metadata struct {
	Name        string
	Description string
	Homepage    string
	Dir         string
	SkillFile   string
	UpdatedAt   time.Time
}

type Catalog struct {
	root string
}

func NewCatalog(root string) (*Catalog, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return nil, errors.New("skills root is required")
	}
	absRoot, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("resolve skills root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create skills root: %w", err)
	}
	return &Catalog{root: absRoot}, nil
}

func (c *Catalog) List() ([]Metadata, error) {
	dirEntries, err := os.ReadDir(c.root)
	if err != nil {
		return nil, fmt.Errorf("read skills root: %w", err)
	}

	var out []Metadata
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(c.root, entry.Name(), "SKILL.md")
		meta, _, err := parseSkillFile(skillPath)
		if err != nil {
			continue
		}
		out = append(out, meta)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (c *Catalog) LoadAll() ([]Skill, error) {
	metaList, err := c.List()
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, meta := range metaList {
		skill, err := LoadSkillFromFile(meta.SkillFile)
		if err == nil {
			out = append(out, skill)
		}
	}
	return out, nil
}

func parseSkillFile(path string) (Metadata, string, error) {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, "", err
	}
	content := string(contentBytes)
	metadataMap, body, err := parseFrontmatter(content)
	if err != nil {
		return Metadata{}, "", fmt.Errorf("parse %s: %w", path, err)
	}

	name := strings.TrimSpace(trimQuotes(metadataMap["name"]))
	description := strings.TrimSpace(trimQuotes(metadataMap["description"]))
	if name == "" {
		return Metadata{}, "", errors.New("missing frontmatter name")
	}
	if description == "" {
		return Metadata{}, "", errors.New("missing frontmatter description")
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		return Metadata{}, "", err
	}

	return Metadata{
		Name:        name,
		Description: description,
		Homepage:    strings.TrimSpace(trimQuotes(metadataMap["homepage"])),
		Dir:         filepath.Dir(path),
		SkillFile:   path,
		UpdatedAt:   fileInfo.ModTime(),
	}, strings.TrimSpace(body), nil
}

func parseFrontmatter(content string) (map[string]string, string, error) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return nil, "", errors.New("frontmatter start '---' not found")
	}

	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", errors.New("frontmatter must start at first line")
	}

	frontmatter := make(map[string]string)
	i := 1
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			i++
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		frontmatter[key] = value
	}
	if i >= len(lines) {
		return nil, "", errors.New("frontmatter end '---' not found")
	}

	body := strings.Join(lines[i:], "\n")
	return frontmatter, body, nil
}

func trimQuotes(s string) string {
	if len(s) >= 2 && ((strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) || (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'"))) {
		return s[1 : len(s)-1]
	}
	return s
}

// RenderBlock formats loaded skills into a system prompt instruction block.
func RenderBlock(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, skill := range skills {
		sb.WriteString(fmt.Sprintf("## Skill: %s\n", skill.Name))
		sb.WriteString(fmt.Sprintf("Description: %s\n\n", skill.Description))
		sb.WriteString("Instructions:\n")
		for _, step := range skill.Steps {
			sb.WriteString(fmt.Sprintf("%d. %s\n", step.Index, step.Instruction))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
