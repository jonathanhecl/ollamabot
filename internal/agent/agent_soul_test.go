package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateSoulFromPrompt(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Use a temp dir so tests never touch the repo's runtime agent folder.
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "agent"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(origDir)
	}()

	if err := EnsureSoulDirAndFile(); err != nil {
		t.Fatal(err)
	}

	t.Run("short directive sets mood", func(t *testing.T) {
		if err := UpdateSoulFromPrompt("se feliz"); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join("agent", "SOUL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(string(content)), "emotional tone: cheerful and positive") {
			t.Fatalf("expected cheerful mood in SOUL, got:\n%s", content)
		}
	})

	t.Run("conversational message must not change mood", func(t *testing.T) {
		// Write a stable SOUL first.
		base := "# Identity\n\n- Name: OllamaBot\n- Emotional tone: professional and pragmatic\n\nCore instructions."
		if err := os.WriteFile(filepath.Join("agent", "SOUL.md"), []byte(base), 0644); err != nil {
			t.Fatal(err)
		}
		if err := UpdateSoulFromPrompt("estoy feliz con el resultado del proyecto"); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join("agent", "SOUL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "Emotional tone: professional and pragmatic") {
			t.Fatalf("conversational 'feliz' must not change mood, got:\n%s", content)
		}
		if strings.Contains(strings.ToLower(string(content)), "cheerful") {
			t.Fatalf("mood changed to cheerful on a conversational message:\n%s", content)
		}
	})

	t.Run("directive sets name", func(t *testing.T) {
		if err := UpdateSoulFromPrompt("tu nombre es Nova"); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join("agent", "SOUL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "- Name: Nova") {
			t.Fatalf("expected name Nova in SOUL, got:\n%s", content)
		}
	})
}
