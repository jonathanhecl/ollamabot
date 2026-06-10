package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSupportsCapability_thinking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe-cache.json")
	payload := `{
  "models": [
    {
      "Name": "granite4.1:8b",
      "Capabilities": { "thinking": "pendiente", "completion": "comprobado" }
    },
    {
      "Name": "freehuntx/qwen3-coder:8b",
      "Capabilities": { "thinking": "comprobado", "completion": "comprobado" }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	if SupportsCapability(path, "granite4.1:8b", "thinking") {
		t.Fatal("granite should not support thinking")
	}
	if !SupportsCapability(path, "freehuntx/qwen3-coder:8b", "thinking") {
		t.Fatal("qwen3-coder should support thinking")
	}
	if SupportsCapability(path, "unknown-model", "thinking") {
		t.Fatal("unknown model should not assume thinking")
	}
}

func TestSupportsCapability_missingSnapshot(t *testing.T) {
	if SupportsCapability(filepath.Join(t.TempDir(), "missing.json"), "any", "thinking") {
		t.Fatal("missing snapshot should return false")
	}
}

func TestModelNamesMatch(t *testing.T) {
	if !modelNamesMatch("foo:latest", "foo") {
		t.Fatal("expected latest tag tolerance")
	}
}

func TestChecker_unknownModelTrustsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe-cache.json")
	payload := `{"models":[{"Name":"known","Capabilities":{"thinking":"comprobado"}}]}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	check := Checker(path)
	if check == nil {
		t.Fatal("expected checker")
	}
	if !check("unknown-model", "thinking") {
		t.Fatal("checker should trust unknown models")
	}
	if !check("known", "thinking") {
		t.Fatal("known model should have thinking")
	}
}
