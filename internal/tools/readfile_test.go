package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileNormal(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "test.txt"), []byte("file contents"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	content, err := ReadFile(ws, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if content != "file contents" {
		t.Fatalf("expected 'file contents', got %q", content)
	}
}

func TestReadFileDirectory(t *testing.T) {
	ws := t.TempDir()
	subDir := filepath.Join(ws, "subdir")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "a.txt"), []byte("a"), 0644)
	_ = os.WriteFile(filepath.Join(subDir, "b.txt"), []byte("b"), 0644)
	_ = os.MkdirAll(filepath.Join(subDir, "nested"), 0755)

	content, err := ReadFile(ws, "subdir")
	if err != nil {
		t.Fatalf("ReadFile dir: %v", err)
	}

	// Should list entries
	if content == "" {
		t.Fatal("expected directory listing, got empty string")
	}
	// Should contain file names
	if !containsSubstring(content, "a.txt") || !containsSubstring(content, "b.txt") {
		t.Fatalf("expected directory listing with a.txt and b.txt, got:\n%s", content)
	}
	// Directories should have trailing /
	if !containsSubstring(content, "nested/") {
		t.Fatalf("expected nested/ in listing, got:\n%s", content)
	}
}

func TestReadFileNotFound(t *testing.T) {
	ws := t.TempDir()

	_, err := ReadFile(ws, "nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadFilePathTraversal(t *testing.T) {
	ws := t.TempDir()

	_, err := ReadFile(ws, "../escape.txt")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestReadFileBinaryRejected(t *testing.T) {
	ws := t.TempDir()
	// Write a file containing null bytes (binary)
	binaryContent := []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x00, 0x57, 0x6F, 0x72, 0x6C, 0x64}
	if err := os.WriteFile(filepath.Join(ws, "binary.dat"), binaryContent, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := ReadFile(ws, "binary.dat")
	if err == nil {
		t.Fatal("expected error for binary file")
	}
}

func TestReadFileEmptyFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "empty.txt"), []byte{}, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	content, err := ReadFile(ws, "empty.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if content != "" {
		t.Fatalf("expected empty string, got %q", content)
	}
}

func TestReadFileTooLarge(t *testing.T) {
	ws := t.TempDir()
	// Create a file just over the 1MiB limit
	data := make([]byte, maxReadFileSize+100)
	for i := range data {
		data[i] = 'A'
	}
	if err := os.WriteFile(filepath.Join(ws, "large.txt"), data, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := ReadFile(ws, "large.txt")
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
