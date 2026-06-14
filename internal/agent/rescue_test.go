package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathMemoryRescue(t *testing.T) {
	temp, err := os.MkdirTemp("", "ollamabot-rescue-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(temp)

	targetFile := filepath.Join(temp, "target_source.go")
	if err := os.WriteFile(targetFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	pm := newPathMemory(temp)

	// Remember a successful tool parameter call
	pm.RememberToolResult("Write", map[string]any{"file_path": targetFile}, "success", false)

	// 1. Absolute path already exists
	abs, rescued, ok := pm.Resolve(targetFile)
	if !ok || rescued || abs != targetFile {
		t.Errorf("expected clean absolute resolution, got abs=%q, rescued=%v, ok=%v", abs, rescued, ok)
	}

	// 2. Relative path joining directly
	rel := "target_source.go"
	abs, rescued, ok = pm.Resolve(rel)
	if !ok || rescued || abs != targetFile {
		t.Errorf("expected relative resolution, got abs=%q, rescued=%v, ok=%v", abs, rescued, ok)
	}

	// 3. Basename rescue from memory
	abs, rescued, ok = pm.Resolve("target_source.go")
	if !ok || abs != targetFile {
		t.Errorf("expected path memory rescue, got abs=%q, rescued=%v, ok=%v", abs, rescued, ok)
	}
}

func TestPathMemoryWalkAndSuggestions(t *testing.T) {
	temp, err := os.MkdirTemp("", "ollamabot-suggestions-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(temp)

	nestedDir := filepath.Join(temp, "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	file1 := filepath.Join(temp, "helper.py")
	file2 := filepath.Join(nestedDir, "helper_test.py")
	file3 := filepath.Join(temp, "main.go")

	_ = os.WriteFile(file1, []byte("print('hello')"), 0644)
	_ = os.WriteFile(file2, []byte("print('test')"), 0644)
	_ = os.WriteFile(file3, []byte("package main"), 0644)

	pm := newPathMemory(temp)

	suggs := pm.FindSuggestions("helper.py")
	if len(suggs) < 1 {
		t.Errorf("expected suggestions for helper.py, got %d", len(suggs))
	}
	hasHelper := false
	for _, s := range suggs {
		if filepath.Base(s) == "helper.py" {
			hasHelper = true
		}
	}
	if !hasHelper {
		t.Errorf("expected helper.py in suggestions, got %v", suggs)
	}

	suggsFuzzy := pm.FindSuggestions("help")
	if len(suggsFuzzy) < 2 {
		t.Errorf("expected at least 2 suggestions for 'help', got %d: %v", len(suggsFuzzy), suggsFuzzy)
	}
}

