package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveAndValidatePath resolves rawPath against workspace and checks bounds.
func ResolveAndValidatePath(workspace, rawPath string) (string, error) {
	clean := filepath.Clean(rawPath)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path not allowed (contains ..)")
	}
	abs := filepath.Join(workspace, clean)

	wsReal, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		wsReal = workspace
	}

	// Resolve the absolute path
	absReal, err := filepath.Abs(abs)
	if err != nil {
		absReal = abs
	}

	if !strings.HasPrefix(absReal, wsReal) {
		return "", fmt.Errorf("path escapes workspace")
	}

	// Safety: protect system folders or specific hidden files
	lower := strings.ToLower(absReal)
	if strings.Contains(lower, ".git"+string(filepath.Separator)) {
		return "", fmt.Errorf("writing inside .git directory is not allowed")
	}

	return absReal, nil
}

// WriteFile writes a file atomically within the workspace.
func WriteFile(workspace, rawPath, contents string) error {
	path, err := ResolveAndValidatePath(workspace, rawPath)
	if err != nil {
		return err
	}

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmp, err := os.CreateTemp(parent, "*.write.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to overwrite target file: %w", err)
	}

	return nil
}
