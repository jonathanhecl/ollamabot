package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndValidatePathNormal(t *testing.T) {
	ws := t.TempDir()

	abs, err := ResolveAndValidatePath(ws, "foo/bar.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(abs, ws) {
		t.Fatalf("resolved path %q does not start with workspace %q", abs, ws)
	}
}

func TestResolveAndValidatePathRejectsDotDot(t *testing.T) {
	ws := t.TempDir()

	_, err := ResolveAndValidatePath(ws, "../escape.txt")
	if err == nil {
		t.Fatal("expected error for path with ..")
	}
}

func TestResolveAndValidatePathRejectsGitDir(t *testing.T) {
	ws := t.TempDir()
	gitDir := filepath.Join(ws, ".git")
	_ = os.MkdirAll(gitDir, 0755)

	_, err := ResolveAndValidatePath(ws, ".git/config")
	if err == nil {
		t.Fatal("expected error for .git path")
	}
}

func TestWriteFileCreatesParentDirs(t *testing.T) {
	ws := t.TempDir()

	err := WriteFile(ws, "deep/nested/dir/file.txt", "hello")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	path := filepath.Join(ws, "deep", "nested", "dir", "file.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(data))
	}
}

func TestWriteFileOverwrites(t *testing.T) {
	ws := t.TempDir()

	if err := WriteFile(ws, "test.txt", "first"); err != nil {
		t.Fatalf("WriteFile first: %v", err)
	}
	if err := WriteFile(ws, "test.txt", "second"); err != nil {
		t.Fatalf("WriteFile second: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(ws, "test.txt"))
	if string(data) != "second" {
		t.Fatalf("expected 'second', got %q", string(data))
	}
}

func TestEditFileSingleOccurrence(t *testing.T) {
	ws := t.TempDir()
	if err := WriteFile(ws, "edit.txt", "hello world"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	diff, err := EditFile(ws, "edit.txt", "hello", "goodbye", false)
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}

	data, _ := os.ReadFile(filepath.Join(ws, "edit.txt"))
	if string(data) != "goodbye world" {
		t.Fatalf("expected 'goodbye world', got %q", string(data))
	}
}

func TestEditFileMultipleOccurrenceWithoutReplaceAll(t *testing.T) {
	ws := t.TempDir()
	if err := WriteFile(ws, "multi.txt", "abc abc abc"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := EditFile(ws, "multi.txt", "abc", "xyz", false)
	if err == nil {
		t.Fatal("expected error for multiple matches without replace_all")
	}
	if !strings.Contains(err.Error(), "matched 3 times") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestEditFileMultipleOccurrenceWithReplaceAll(t *testing.T) {
	ws := t.TempDir()
	if err := WriteFile(ws, "multi.txt", "abc abc abc"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := EditFile(ws, "multi.txt", "abc", "xyz", true)
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(ws, "multi.txt"))
	if string(data) != "xyz xyz xyz" {
		t.Fatalf("expected 'xyz xyz xyz', got %q", string(data))
	}
}

func TestEditFileNotFound(t *testing.T) {
	ws := t.TempDir()
	if err := WriteFile(ws, "exist.txt", "content"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := EditFile(ws, "exist.txt", "nonexistent", "new", false)
	if err == nil {
		t.Fatal("expected error for string not found")
	}
}

func TestEditFilePathTraversal(t *testing.T) {
	ws := t.TempDir()
	_, err := EditFile(ws, "../escape.txt", "a", "b", false)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}
