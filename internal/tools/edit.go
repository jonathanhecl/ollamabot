package tools

import (
	"fmt"
	"os"
	"strings"
)

// unifiedDiff generates a simple unified diff between strings a and b.
func unifiedDiff(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	i := 0
	for i < len(al) && i < len(bl) && al[i] == bl[i] {
		i++
	}
	j, k := len(al)-1, len(bl)-1
	for j >= i && k >= i && al[j] == bl[k] {
		j--
		k--
	}
	if i > j && i > k {
		return ""
	}
	const c = 3
	lo := max(0, i-c)
	ro := min(len(al), j+1+c)
	rb := min(len(bl), k+1+c)
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", lo+1, ro-lo, lo+1, rb-lo)
	o, n := lo, lo
	for o < ro || n < rb {
		if o < ro && n < rb && al[o] == bl[n] && (o < i || o > j) {
			fmt.Fprintf(&sb, " %s\n", al[o])
			o++
			n++
			continue
		}
		if o <= j && o < ro {
			fmt.Fprintf(&sb, "-%s\n", al[o])
			o++
		} else if n <= k && n < rb {
			fmt.Fprintf(&sb, "+%s\n", bl[n])
			n++
		} else if o < ro {
			fmt.Fprintf(&sb, " %s\n", al[o])
			o++
			n++
		} else {
			fmt.Fprintf(&sb, "+%s\n", bl[n])
			n++
		}
	}
	return sb.String()
}

// splitLines splits content into lines, removing trailing carriage returns and dropping the final empty string if caused by a trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// normalizeLine removes \r, trims leading/trailing spaces/tabs, and collapses multiple spaces/tabs into a single space.
func normalizeLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimSpace(s)
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

// findFuzzyMatches finds all occurrences where fileLines normalized matches oldLines normalized, returning the starting indices.
func findFuzzyMatches(fileLines, oldLines []string) []int {
	var matches []int
	if len(oldLines) == 0 || len(fileLines) < len(oldLines) {
		return nil
	}

	normOld := make([]string, len(oldLines))
	for i, line := range oldLines {
		normOld[i] = normalizeLine(line)
	}

	normFile := make([]string, len(fileLines))
	for i, line := range fileLines {
		normFile[i] = normalizeLine(line)
	}

	for i := 0; i <= len(normFile)-len(normOld); i++ {
		match := true
		for j := 0; j < len(normOld); j++ {
			if normFile[i+j] != normOld[j] {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, i)
		}
	}
	return matches
}

// EditFile replaces oldString with newString in rawPath inside workspace.
// Returns the resulting unified diff and any error.
func EditFile(workspace, rawPath, oldString, newString string, replaceAll bool) (string, error) {
	path, err := ResolveAndValidatePath(workspace, rawPath)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)
	count := strings.Count(content, oldString)
	if count > 0 {
		if count > 1 && !replaceAll {
			return "", fmt.Errorf("old_string matched %d times; set replace_all=true to replace all occurrences", count)
		}

		limit := 1
		if replaceAll {
			limit = -1
		}

		updated := strings.Replace(content, oldString, newString, limit)
		if err := WriteFile(workspace, rawPath, updated); err != nil {
			return "", err
		}

		diff := unifiedDiff(content, updated)
		return diff, nil
	}

	// Fallback to fuzzy matching
	fileLines := splitLines(content)
	oldLines := splitLines(oldString)
	if len(oldLines) == 0 {
		return "", fmt.Errorf("old_string not found in file (empty or whitespace only)")
	}

	matches := findFuzzyMatches(fileLines, oldLines)
	if len(matches) == 0 {
		return "", fmt.Errorf("old_string not found in file (checked exact and fuzzy matches)")
	}

	if len(matches) > 1 && !replaceAll {
		return "", fmt.Errorf("old_string matched %d times fuzzily; set replace_all=true to replace all occurrences", len(matches))
	}

	newLines := splitLines(newString)

	// Reconstruct updated lines in reverse order to avoid shifting indices.
	updatedLines := make([]string, len(fileLines))
	copy(updatedLines, fileLines)

	for idx := len(matches) - 1; idx >= 0; idx-- {
		start := matches[idx]
		end := start + len(oldLines)

		tail := make([]string, len(updatedLines)-end)
		copy(tail, updatedLines[end:])

		updatedLines = append(updatedLines[:start], newLines...)
		updatedLines = append(updatedLines, tail...)
	}

	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}

	updated := strings.Join(updatedLines, lineEnding)
	if strings.HasSuffix(content, "\n") {
		updated += lineEnding
	}

	if err := WriteFile(workspace, rawPath, updated); err != nil {
		return "", err
	}

	diff := unifiedDiff(content, updated)
	return diff, nil
}
