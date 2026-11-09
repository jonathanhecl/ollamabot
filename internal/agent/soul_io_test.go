package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdirTemp(t *testing.T) {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})
}

func TestSaveSoulCreatesDefaultAndBacksUp(t *testing.T) {
	chdirTemp(t)

	if _, err := SoulPath(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("agent", "SOUL.md")); err != nil {
		t.Fatalf("expected default soul file: %v", err)
	}

	custom := "# Identity\n\n- Name: Nova\n"
	if err := SaveSoul(custom); err != nil {
		t.Fatal(err)
	}

	content, err := LoadSoul()
	if err != nil {
		t.Fatal(err)
	}
	if content != custom {
		t.Fatalf("LoadSoul = %q, want %q", content, custom)
	}
	if IsDefaultSoul(content) {
		t.Fatal("custom soul reported as default")
	}

	backup, err := os.ReadFile(filepath.Join("agent", "backups", "SOUL.md.bak1"))
	if err != nil {
		t.Fatalf("expected rolling backup: %v", err)
	}
	if !strings.Contains(string(backup), "Core Truths") {
		t.Fatalf("backup does not hold the previous default soul")
	}

	if err := SaveSoul("   "); err == nil {
		t.Fatal("expected empty soul to be rejected")
	}
}

func TestResetSoulRestoresDefault(t *testing.T) {
	chdirTemp(t)

	if err := SaveSoul("# Custom\n"); err != nil {
		t.Fatal(err)
	}
	if err := ResetSoul(); err != nil {
		t.Fatal(err)
	}
	content, err := LoadSoul()
	if err != nil {
		t.Fatal(err)
	}
	if !IsDefaultSoul(content) {
		t.Fatalf("expected default soul after reset")
	}
}

func TestSoulPathPrefersExistingLowercaseFile(t *testing.T) {
	chdirTemp(t)

	if err := os.MkdirAll("agent", 0o755); err != nil {
		t.Fatal(err)
	}
	altPath := filepath.Join("agent", "soul.md")
	if err := os.WriteFile(altPath, []byte("# lowercase\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := SoulPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != altPath {
		t.Fatalf("SoulPath = %q, want %q", path, altPath)
	}
	if err := SaveSoul("# updated\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("agent", "SOUL.md")); !os.IsNotExist(err) {
		t.Fatalf("SaveSoul must not create SOUL.md when soul.md exists")
	}
	content, err := os.ReadFile(altPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# updated\n" {
		t.Fatalf("lowercase soul not updated: %q", content)
	}
}
