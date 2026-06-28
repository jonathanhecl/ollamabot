package tools

import (
	"fmt"
	"os"
	"strings"
)

// ApplyDiff applies a unified diff to a file in the workspace.
// The diff must be in standard unified diff format with --- and +++ headers
// and @@ hunk headers.
func ApplyDiff(workspace, rawPath, diffContent string) (string, error) {
	path, err := ResolveAndValidatePath(workspace, rawPath)
	if err != nil {
		return "", err
	}

	original, err := readFileRaw(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	origLines := splitLines(original)
	hunks, err := parseUnifiedDiff(diffContent)
	if err != nil {
		return "", err
	}

	result, err := applyHunks(origLines, hunks)
	if err != nil {
		return "", err
	}

	newContent := strings.Join(result, "\n") + "\n"
	if newContent == original {
		return "No changes applied.", nil
	}

	if err := writeFileRaw(path, newContent); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Diff applied successfully to %s (%d hunks).", rawPath, len(hunks)), nil
}

type hunk struct {
	oldStart int
	oldLen   int
	newStart int
	newLen   int
	lines    []diffLine
}

type diffLine struct {
	kind    byte // ' ', '+', '-'
	content string
}

func parseUnifiedDiff(diffContent string) ([]hunk, error) {
	lines := splitLines(diffContent)
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty diff")
	}

	var hunks []hunk
	i := 0

	// Skip file headers (--- and +++) and any preamble
	for i < len(lines) {
		if strings.HasPrefix(lines[i], "@@") {
			break
		}
		i++
	}

	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "@@") {
			i++
			continue
		}

		h, err := parseHunkHeader(lines[i])
		if err != nil {
			return nil, err
		}

		i++
		for i < len(lines) {
			if strings.HasPrefix(lines[i], "@@") {
				break
			}
			if strings.HasPrefix(lines[i], "---") || strings.HasPrefix(lines[i], "+++") {
				break
			}
			if len(lines[i]) == 0 {
				i++
				continue
			}
			kind := lines[i][0]
			if kind != ' ' && kind != '+' && kind != '-' && kind != '\\' {
				break
			}
			if kind == '\\' {
				// "\ No newline at end of file" marker
				i++
				continue
			}
			h.lines = append(h.lines, diffLine{
				kind:    kind,
				content: lines[i][1:],
			})
			i++
		}
		hunks = append(hunks, h)
	}

	if len(hunks) == 0 {
		return nil, fmt.Errorf("no hunks found in diff")
	}
	return hunks, nil
}

func parseHunkHeader(line string) (hunk, error) {
	// Format: @@ -oldStart,oldLen +newStart,newLen @@
	s := strings.TrimPrefix(line, "@@ ")
	parts := strings.SplitN(s, " ", 2)
	if len(parts) < 2 {
		return hunk{}, fmt.Errorf("invalid hunk header: %s", line)
	}

	oldPart := strings.TrimPrefix(parts[0], "-")
	newPart := strings.TrimPrefix(parts[1], "+")

	oldStart, oldLen, err := parseRange(oldPart)
	if err != nil {
		return hunk{}, err
	}
	newStart, newLen, err := parseRange(newPart)
	if err != nil {
		return hunk{}, err
	}

	return hunk{
		oldStart: oldStart,
		oldLen:   oldLen,
		newStart: newStart,
		newLen:   newLen,
	}, nil
}

func parseRange(s string) (int, int, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) == 1 {
		n := 0
		_, err := fmt.Sscanf(parts[0], "%d", &n)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range: %s", s)
		}
		return n, 1, nil
	}
	start, length := 0, 0
	_, err := fmt.Sscanf(parts[0], "%d", &start)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range start: %s", parts[0])
	}
	_, err = fmt.Sscanf(parts[1], "%d", &length)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range length: %s", parts[1])
	}
	return start, length, nil
}

func applyHunks(origLines []string, hunks []hunk) ([]string, error) {
	result := make([]string, 0, len(origLines))
	origIdx := 0

	for _, h := range hunks {
		// Copy lines before the hunk
		targetIdx := h.oldStart - 1
		if targetIdx < 0 {
			targetIdx = 0
		}
		for origIdx < targetIdx && origIdx < len(origLines) {
			result = append(result, origLines[origIdx])
			origIdx++
		}

		// Apply hunk lines
		for _, dl := range h.lines {
			switch dl.kind {
			case ' ':
				if origIdx < len(origLines) {
					result = append(result, origLines[origIdx])
					origIdx++
				}
			case '-':
				if origIdx < len(origLines) {
					origIdx++
				}
			case '+':
				result = append(result, dl.content)
			}
		}
	}

	// Copy remaining lines
	for origIdx < len(origLines) {
		result = append(result, origLines[origIdx])
		origIdx++
	}

	return result, nil
}

func readFileRaw(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeFileRaw(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
