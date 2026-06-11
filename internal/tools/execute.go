package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// defaultAllowedCommands is the set of executables the agent may call without
// additional configuration. Only the base name (no path) is checked.
var defaultAllowedCommands = map[string]bool{
	"ffmpeg":    true,
	"ffprobe":   true,
	"convert":   true,
	"pandoc":    true,
	"python3":   true,
	"python":    true,
	"node":      true,
	"exiftool":  true,
	"mediainfo": true,
	"identify":  true,
}

const maxOutputBytes = 64 * 1024 // 64 KiB

// executeCommand runs an allowed shell command inside the workspace directory
// and returns the combined stdout+stderr output (capped at maxOutputBytes).
func executeCommand(ctx context.Context, workspace, command string, args []string) (string, error) {
	base := command
	if idx := strings.LastIndexAny(command, "/\\"); idx >= 0 {
		base = command[idx+1:]
	}
	if !defaultAllowedCommands[base] {
		return "", fmt.Errorf("command %q is not in the allowed list (%s)", base,
			strings.Join(allowedList(), ", "))
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workspace

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		out := buf.String()
		if len(out) > maxOutputBytes {
			out = out[:maxOutputBytes] + "\n[output truncated]"
		}
		if out != "" {
			return out, nil
		}
		return "", fmt.Errorf("command failed: %w", err)
	}

	out := buf.String()
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n[output truncated]"
	}
	return out, nil
}

func allowedList() []string {
	var list []string
	for k := range defaultAllowedCommands {
		list = append(list, k)
	}
	return list
}
