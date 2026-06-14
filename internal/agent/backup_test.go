package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupFile(t *testing.T) {
	// Clean up any pre-existing agent directories for test isolation
	_ = os.RemoveAll("agent")
	defer os.RemoveAll("agent")

	dummy := "dummy_soul.md"
	_ = os.Remove(dummy)
	defer os.Remove(dummy)

	err := os.WriteFile(dummy, []byte("version 0"), 0644)
	if err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	// First backup
	err = backupFile(dummy)
	if err != nil {
		t.Fatalf("first backup failed: %v", err)
	}

	// Verify bak1 is created
	bak1 := filepath.Join("agent", "backups", "dummy_soul.md.bak1")
	data, err := os.ReadFile(bak1)
	if err != nil {
		t.Fatalf("failed to read bak1: %v", err)
	}
	if string(data) != "version 0" {
		t.Errorf("expected bak1 content to be 'version 0', got %q", string(data))
	}

	// Write new version and backup again
	err = os.WriteFile(dummy, []byte("version 1"), 0644)
	if err != nil {
		t.Fatalf("failed to update dummy: %v", err)
	}

	err = backupFile(dummy)
	if err != nil {
		t.Fatalf("second backup failed: %v", err)
	}

	// Verify bak2 is version 0 and bak1 is version 1
	data1, err := os.ReadFile(bak1)
	if err != nil {
		t.Fatalf("failed to read bak1: %v", err)
	}
	if string(data1) != "version 1" {
		t.Errorf("expected bak1 content to be 'version 1', got %q", string(data1))
	}

	bak2 := filepath.Join("agent", "backups", "dummy_soul.md.bak2")
	data2, err := os.ReadFile(bak2)
	if err != nil {
		t.Fatalf("failed to read bak2: %v", err)
	}
	if string(data2) != "version 0" {
		t.Errorf("expected bak2 content to be 'version 0', got %q", string(data2))
	}
}
