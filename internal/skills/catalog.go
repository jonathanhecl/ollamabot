package skills

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type EmbeddingClient interface {
	Embed(ctx context.Context, req ollama.EmbedRequest) (ollama.EmbedResponse, error)
}

var (
	globalEmbedMu    sync.RWMutex
	globalEmbedCache = make(map[string][]float64)
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

// IndexEmbeddings pre-computes and caches embeddings for all skills in the catalog
// using their name and description.
func (c *Catalog) IndexEmbeddings(ctx context.Context, client EmbeddingClient, embedModel string) error {
	if client == nil || strings.TrimSpace(embedModel) == "" {
		return nil
	}
	metaList, err := c.List()
	if err != nil {
		return err
	}
	for _, meta := range metaList {
		cacheKey := meta.SkillFile + ":" + strconv.FormatInt(meta.UpdatedAt.Unix(), 10)
		globalEmbedMu.RLock()
		_, exists := globalEmbedCache[cacheKey]
		globalEmbedMu.RUnlock()
		if exists {
			continue
		}

		text := fmt.Sprintf("%s: %s", meta.Name, meta.Description)
		resp, err := client.Embed(ctx, ollama.EmbedRequest{
			Model: embedModel,
			Input: text,
		})
		if err != nil || len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
			continue
		}
		globalEmbedMu.Lock()
		globalEmbedCache[cacheKey] = resp.Embeddings[0]
		globalEmbedMu.Unlock()
	}
	return nil
}

// FilterRelevant loads skills from the catalog and returns only those that are semantically
// or textually similar to the user prompt, avoiding injecting unnecessary skill headers.
func (c *Catalog) FilterRelevant(ctx context.Context, client EmbeddingClient, embedModel string, userPrompt string, threshold float64) ([]Skill, error) {
	allSkills, err := c.LoadAll()
	if err != nil || len(allSkills) == 0 {
		return nil, err
	}
	trimmedPrompt := strings.TrimSpace(userPrompt)
	if trimmedPrompt == "" {
		return nil, nil
	}

	metaList, err := c.List()
	if err != nil {
		return nil, err
	}
	metaByName := make(map[string]Metadata, len(metaList))
	for _, m := range metaList {
		metaByName[m.Name] = m
	}

	// 1. Try semantic embedding search if client and model are available
	if client != nil && strings.TrimSpace(embedModel) != "" {
		_ = c.IndexEmbeddings(ctx, client, embedModel)
		resp, err := client.Embed(ctx, ollama.EmbedRequest{
			Model: embedModel,
			Input: trimmedPrompt,
		})
		if err == nil && len(resp.Embeddings) > 0 && len(resp.Embeddings[0]) > 0 {
			queryVec := resp.Embeddings[0]
			var relevant []Skill
			for _, skill := range allSkills {
				meta, ok := metaByName[skill.Name]
				if !ok {
					continue
				}
				cacheKey := meta.SkillFile + ":" + strconv.FormatInt(meta.UpdatedAt.Unix(), 10)
				globalEmbedMu.RLock()
				skillVec, hasVec := globalEmbedCache[cacheKey]
				globalEmbedMu.RUnlock()
				if !hasVec {
					continue
				}
				sim := cosineSimilarity(queryVec, skillVec)
				if sim >= threshold {
					relevant = append(relevant, skill)
				}
			}
			if len(relevant) > 0 {
				return relevant, nil
			}
		}
	}

	// 2. Fallback: keyword matching against skill name and description words
	promptLower := strings.ToLower(trimmedPrompt)
	var relevant []Skill
	for _, skill := range allSkills {
		nameLower := strings.ToLower(skill.Name)
		if strings.Contains(promptLower, nameLower) {
			relevant = append(relevant, skill)
			continue
		}
		// Check description keywords
		descWords := strings.Fields(strings.ToLower(skill.Description))
		matchCount := 0
		for _, w := range descWords {
			if len(w) > 3 && strings.Contains(promptLower, w) {
				matchCount++
			}
		}
		if matchCount >= 2 {
			relevant = append(relevant, skill)
		}
	}
	return relevant, nil
}

func cosineSimilarity(a, b []float64) float64 {
	var dot, normA, normB float64
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		av := a[i]
		bv := b[i]
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
