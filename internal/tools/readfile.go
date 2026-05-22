package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxReadFileSize = 1 << 20 // 1 MiB

// ReadFile reads a text file within the workspace safely.
func ReadFile(workspace, rawPath string) (string, error) {
	clean := filepath.Clean(rawPath)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path not allowed")
	}
	abs := filepath.Join(workspace, clean)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found")
		}
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}
	wsReal, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		wsReal = workspace
	}
	if !strings.HasPrefix(real, wsReal) {
		return "", fmt.Errorf("path escapes workspace")
	}
	info, err := os.Stat(real)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found")
		}
		return "", err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(real)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		for _, e := range entries {
			sb.WriteString(e.Name())
			if e.IsDir() {
				sb.WriteString("/")
			}
			sb.WriteString("\n")
		}
		return sb.String(), nil
	}
	if info.Size() > maxReadFileSize {
		return "", fmt.Errorf("file too large (%d bytes, max %d)", info.Size(), maxReadFileSize)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", err
	}
	if !isText(data) {
		return "", fmt.Errorf("file appears to be binary")
	}
	return string(data), nil
}

func isText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}
