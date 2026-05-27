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
	if count == 0 {
		return "", fmt.Errorf("old_string not found in file")
	}
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
