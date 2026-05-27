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
