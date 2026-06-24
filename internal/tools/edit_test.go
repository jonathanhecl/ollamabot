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

func TestResolveAndValidatePathAllowsWorkspaceAbsolutePath(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "foo", "bar.txt")

	abs, err := ResolveAndValidatePath(ws, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if abs != target {
		t.Fatalf("expected %q, got %q", target, abs)
	}
}

func TestResolveAndValidatePathRejectsAbsoluteEscape(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")

	_, err := ResolveAndValidatePath(ws, outside)
	if err == nil {
		t.Fatal("expected error for absolute path outside workspace")
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

func TestEditFileFuzzySingleOccurrence(t *testing.T) {
	ws := t.TempDir()
	content := "func hello() {\n\tfmt.Println(\"world\")\n}\n"
	if err := WriteFile(ws, "fuzzy.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// oldString has 4 spaces indent, while file has tab indent.
	oldString := "func hello() {\n    fmt.Println(\"world\")\n}"
	newString := "func hello() {\n    fmt.Println(\"goodbye\")\n}"

	diff, err := EditFile(ws, "fuzzy.txt", oldString, newString, false)
	if err != nil {
		t.Fatalf("EditFile fuzzy: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}

	data, _ := os.ReadFile(filepath.Join(ws, "fuzzy.txt"))
	expected := "func hello() {\n    fmt.Println(\"goodbye\")\n}\n"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, string(data))
	}
}

func TestEditFileFuzzyMultipleOccurrence(t *testing.T) {
	ws := t.TempDir()
	content := "func hi() {\n\tfmt.Println(\"hi\")\n}\nfunc hi() {\n\tfmt.Println(\"hi\")\n}\n"
	if err := WriteFile(ws, "fuzzy_multi.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// 2 spaces indent instead of tab
	oldString := "func hi() {\n  fmt.Println(\"hi\")\n}"
	newString := "func hi() {\n  fmt.Println(\"hello\")\n}"

	// 1. Without replaceAll, should fail due to ambiguity
	_, err := EditFile(ws, "fuzzy_multi.txt", oldString, newString, false)
	if err == nil {
		t.Fatal("expected error for multiple fuzzy matches without replaceAll")
	}
	if !strings.Contains(err.Error(), "matched 2 times fuzzily") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. With replaceAll, should succeed
	_, err = EditFile(ws, "fuzzy_multi.txt", oldString, newString, true)
	if err != nil {
		t.Fatalf("EditFile fuzzy multi replaceAll: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(ws, "fuzzy_multi.txt"))
	expected := "func hi() {\n  fmt.Println(\"hello\")\n}\nfunc hi() {\n  fmt.Println(\"hello\")\n}\n"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, string(data))
	}
}

func TestEditFileFuzzyLineEndings(t *testing.T) {
	// Test CRLF preservation
	ws := t.TempDir()
	contentCRLF := "line1\r\nline2\r\nline3\r\n"
	if err := WriteFile(ws, "crlf.txt", contentCRLF); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// oldString has LF only
	_, err := EditFile(ws, "crlf.txt", "line2\n", "line_two\n", false)
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(ws, "crlf.txt"))
	expectedCRLF := "line1\r\nline_two\r\nline3\r\n"
	if string(data) != expectedCRLF {
		t.Fatalf("expected %q, got %q", expectedCRLF, string(data))
	}

	// Test no trailing newline preservation
	contentNoNL := "line1\nline2"
	if err := WriteFile(ws, "nonl.txt", contentNoNL); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = EditFile(ws, "nonl.txt", "line2", "line_two", false)
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}

	dataNoNL, _ := os.ReadFile(filepath.Join(ws, "nonl.txt"))
	expectedNoNL := "line1\nline_two"
	if string(dataNoNL) != expectedNoNL {
		t.Fatalf("expected %q, got %q", expectedNoNL, string(dataNoNL))
	}
}
