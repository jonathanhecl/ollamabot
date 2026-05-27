package skills

import (
	"os"
	"path/filepath"
	"testing"
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
