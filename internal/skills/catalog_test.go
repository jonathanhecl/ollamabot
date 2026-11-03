package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestCatalogListAndLoad(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "weather", `---
name: weather
description: Weather helper
homepage: https://example.com/weather
---
## Description
Weather helper

## Steps
- Live weather search
`)

	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatalf("new catalog failed: %v", err)
	}

	list, err := catalog.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("unexpected skill count: %d", len(list))
	}
	if list[0].Name != "weather" {
		t.Fatalf("unexpected name: %s", list[0].Name)
	}
	if list[0].Description != "Weather helper" {
		t.Fatalf("unexpected description: %s", list[0].Description)
	}

	skills, err := catalog.LoadAll()
	if err != nil {
		t.Fatalf("load all failed: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "weather" {
		t.Fatalf("unexpected loaded name: %s", skills[0].Name)
	}
	if len(skills[0].Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(skills[0].Steps))
	}
}

func TestCatalogReflectsRealtimeChanges(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "weather", `---
name: weather
description: v1
---
## Steps
- step 1
`)

	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatalf("new catalog failed: %v", err)
	}

	first, err := catalog.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(first) != 1 || first[0].Description != "v1" {
		t.Fatalf("unexpected initial metadata: %#v", first)
	}

	writeSkill(t, root, "weather", `---
name: weather
description: v2
---
## Steps
- step 2
`)
	writeSkill(t, root, "github", `---
name: github
description: gh helper
---
## Steps
- step 3
`)

	second, err := catalog.List()
	if err != nil {
		t.Fatalf("list after update failed: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("expected 2 skills after update, got %d", len(second))
	}

	var weather Metadata
	for _, item := range second {
		if item.Name == "weather" {
			weather = item
			break
		}
	}
	if weather.Name == "" {
		t.Fatalf("weather not found after refresh")
	}
	if weather.Description != "v2" {
		t.Fatalf("expected updated description v2, got %s", weather.Description)
	}
}

func writeSkill(t *testing.T, root string, dir string, content string) {
	t.Helper()
	skillDir := filepath.Join(root, dir)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
}

type mockEmbedClient struct {
	vectors map[string][]float64
}

func (m *mockEmbedClient) Embed(ctx context.Context, req ollama.EmbedRequest) (ollama.EmbedResponse, error) {
	inputStr, _ := req.Input.(string)
	for key, vec := range m.vectors {
		if strings.Contains(strings.ToLower(inputStr), strings.ToLower(key)) {
			return ollama.EmbedResponse{Embeddings: [][]float64{vec}}, nil
		}
	}
	// Default vector orthogonal to target
	return ollama.EmbedResponse{Embeddings: [][]float64{{0.0, 0.0, 1.0}}}, nil
}

func TestCatalogFilterRelevant_Embeddings(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "weather", `---
name: weather
description: Get current temperature and weather forecast
---
## Steps
- 1. Search weather
`)
	writeSkill(t, root, "git", `---
name: git
description: Git version control helper
---
## Steps
- 1. Run git status
`)

	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatalf("NewCatalog failed: %v", err)
	}

	mock := &mockEmbedClient{
		vectors: map[string][]float64{
			"weather":     {1.0, 0.0, 0.0},
			"temperature": {0.95, 0.05, 0.0},
			"git":         {0.0, 1.0, 0.0},
		},
	}

	// Query about temperature -> should match weather (cos sim ~ 0.95) and not git
	relevant, err := catalog.FilterRelevant(context.Background(), mock, "test-embed", "What is the temperature today?", 0.40)
	if err != nil {
		t.Fatalf("FilterRelevant failed: %v", err)
	}
	if len(relevant) != 1 {
		t.Fatalf("expected 1 relevant skill, got %d", len(relevant))
	}
	if relevant[0].Name != "weather" {
		t.Fatalf("expected 'weather' skill, got %q", relevant[0].Name)
	}

	// Query about completely unrelated topic -> no skills included
	unrelated, err := catalog.FilterRelevant(context.Background(), mock, "test-embed", "tell me a random joke", 0.80)
	if err != nil {
		t.Fatalf("FilterRelevant unrelated failed: %v", err)
	}
	if len(unrelated) != 0 {
		t.Fatalf("expected 0 relevant skills for unrelated query, got %d", len(unrelated))
	}
}

func TestCatalogFilterRelevant_KeywordFallback(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "database", `---
name: database
description: Manage SQL queries and database migrations
---
## Steps
- 1. Execute query
`)

	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatalf("NewCatalog failed: %v", err)
	}

	// Without embedding client, matches keyword "database"
	relevant, err := catalog.FilterRelevant(context.Background(), nil, "", "I need to fix the database tables", 0.40)
	if err != nil {
		t.Fatalf("FilterRelevant fallback failed: %v", err)
	}
	if len(relevant) != 1 || relevant[0].Name != "database" {
		t.Fatalf("expected 'database' skill via keyword fallback, got %v", relevant)
	}

	// Unrelated query matches nothing
	unrelated, err := catalog.FilterRelevant(context.Background(), nil, "", "hello world", 0.40)
	if err != nil {
		t.Fatalf("FilterRelevant fallback failed: %v", err)
	}
	if len(unrelated) != 0 {
		t.Fatalf("expected 0 skills for unrelated fallback query, got %d", len(unrelated))
	}
}
