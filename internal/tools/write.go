package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/skills"
)

// ResolveAndValidatePath resolves rawPath against workspace and checks bounds.
func ResolveAndValidatePath(workspace, rawPath string) (string, error) {
	clean := filepath.Clean(rawPath)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("path not allowed (absolute path)")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path not allowed (contains ..)")
	}

	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		wsAbs = workspace
	}
	abs := filepath.Join(wsAbs, clean)

	absReal, err := filepath.Abs(abs)
	if err != nil {
		absReal = abs
	}
	wsReal, err := filepath.EvalSymlinks(wsAbs)
	if err != nil {
		wsReal = wsAbs
	}
	checkPath := absReal
	checkWorkspace := wsAbs
	if resolved, err := filepath.EvalSymlinks(absReal); err == nil {
		checkPath = resolved
		checkWorkspace = wsReal
	}

	rel, err := filepath.Rel(checkWorkspace, checkPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
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

	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, "skill.md") {
		if _, err := skills.ParseSkillMarkdown(contents); err != nil {
			return fmt.Errorf("skill validation failed: %w", err)
		}
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
