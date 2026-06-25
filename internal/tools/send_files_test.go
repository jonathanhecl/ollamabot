package tools

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mockAttachmentHandler struct {
	sessionID string
	ref       string
	mime      string
	path      string
	called    bool
}

func (m *mockAttachmentHandler) OnAttachmentGenerated(sessionID string, ref string, mime string, path string) {
	m.sessionID = sessionID
	m.ref = ref
	m.mime = mime
	m.path = path
	m.called = true
}

func TestSendFilesSingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "workspace")
	sessionsDir := filepath.Join(tmpDir, "sessions")
	_ = os.MkdirAll(workspace, 0755)
	_ = os.MkdirAll(sessionsDir, 0755)

	// Create test file in workspace
	testFile := "hello.txt"
	testContent := "Hello, World!"
	srcPath := filepath.Join(workspace, testFile)
	if err := os.WriteFile(srcPath, []byte(testContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	handler := &mockAttachmentHandler{}
	sessionID := "sess_123"

	msg, err := SendFiles(workspace, sessionsDir, sessionID, []string{testFile}, "", handler)
	if err != nil {
		t.Fatalf("SendFiles failed: %v", err)
	}

	if !strings.Contains(msg, "hello.txt") {
		t.Errorf("expected success message containing hello.txt, got %q", msg)
	}

	if !handler.called {
		t.Fatalf("expected attachment handler to be called")
	}

	if handler.sessionID != sessionID {
		t.Errorf("expected sessionID %q, got %q", sessionID, handler.sessionID)
	}
	if handler.ref != "hello.txt" {
		t.Errorf("expected ref hello.txt, got %q", handler.ref)
	}
	if handler.mime != "text/plain" {
		t.Errorf("expected MIME text/plain, got %q", handler.mime)
	}

	// Verify file was copied to sessionsDir/sessionID/uploads
	destPath := filepath.Join(sessionsDir, sessionID, "uploads", "hello.txt")
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("expected copied file to exist at %s, but got error: %v", destPath, err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read copied file: %v", err)
	}
	if string(data) != testContent {
		t.Errorf("expected content %q, got %q", testContent, string(data))
	}
}

func TestSendFilesMultipleAndDirZipping(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "workspace")
	sessionsDir := filepath.Join(tmpDir, "sessions")
	_ = os.MkdirAll(workspace, 0755)
	_ = os.MkdirAll(sessionsDir, 0755)

	// Create structure:
	// workspace/file1.txt
	// workspace/dir1/file2.txt
	_ = os.MkdirAll(filepath.Join(workspace, "dir1"), 0755)
	_ = os.WriteFile(filepath.Join(workspace, "file1.txt"), []byte("file1"), 0644)
	_ = os.WriteFile(filepath.Join(workspace, "dir1", "file2.txt"), []byte("file2"), 0644)

	handler := &mockAttachmentHandler{}
	sessionID := "sess_456"

	msg, err := SendFiles(workspace, sessionsDir, sessionID, []string{"file1.txt", "dir1"}, "test_archive.zip", handler)
	if err != nil {
		t.Fatalf("SendFiles failed: %v", err)
	}

	if !strings.Contains(msg, "test_archive.zip") {
		t.Errorf("expected success message containing test_archive.zip, got %q", msg)
	}

	if !handler.called {
		t.Fatalf("expected attachment handler to be called")
	}

	if handler.ref != "test_archive.zip" {
		t.Errorf("expected ref test_archive.zip, got %q", handler.ref)
	}
	if handler.mime != "application/zip" {
		t.Errorf("expected MIME application/zip, got %q", handler.mime)
	}

	// Verify zip file exists
	zipPath := filepath.Join(sessionsDir, sessionID, "uploads", "test_archive.zip")
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("expected zip file to exist at %s, but got error: %v", zipPath, err)
	}

	// Read and verify zip content
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("failed to open zip archive: %v", err)
	}
	defer r.Close()

	contents := make(map[string]string)
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open zip file member: %v", err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("failed to read zip file member data: %v", err)
		}
		contents[filepath.ToSlash(f.Name)] = string(data)
	}

	if contents["file1.txt"] != "file1" {
		t.Errorf("expected file1.txt to have content 'file1', got %q", contents["file1.txt"])
	}
	if contents["dir1/file2.txt"] != "file2" {
		t.Errorf("expected dir1/file2.txt to have content 'file2', got %q", contents["dir1/file2.txt"])
	}
}
