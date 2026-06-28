package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SearchFiles performs a regex search across files in the workspace.
// Returns matched lines in file:line:content format.
func SearchFiles(workspace, pattern, searchPath, includeGlob string, maxResults int) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}
	if maxResults <= 0 {
		maxResults = 50
	}

	root, err := ResolveAndValidatePath(workspace, searchPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("path not found: %w", err)
	}
	if !info.IsDir() {
		// Search single file
		return searchSingleFile(root, re, maxResults)
	}

	var matches []string
	count := 0

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if includeGlob != "" {
			matched, _ := filepath.Match(includeGlob, d.Name())
			if !matched {
				return nil
			}
		}
		if count >= maxResults {
			return filepath.SkipAll
		}

		fileMatches, fileCount, err := searchInFile(path, re, maxResults-count)
		if err != nil {
			return nil
		}
		if len(fileMatches) > 0 {
			relPath, _ := filepath.Rel(workspace, path)
			for _, m := range fileMatches {
				matches = append(matches, fmt.Sprintf("%s:%s", relPath, m))
			}
			count += fileCount
		}
		return nil
	})

	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "No matches found.", nil
	}
	result := strings.Join(matches, "\n")
	if count >= maxResults {
		result += fmt.Sprintf("\n... (truncated at %d matches)", maxResults)
	}
	return result, nil
}

func searchSingleFile(path string, re *regexp.Regexp, maxResults int) (string, error) {
	matches, count, err := searchInFile(path, re, maxResults)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "No matches found.", nil
	}
	result := strings.Join(matches, "\n")
	if count >= maxResults {
		result += fmt.Sprintf("\n... (truncated at %d matches)", maxResults)
	}
	return result, nil
}

func searchInFile(path string, re *regexp.Regexp, maxResults int) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if !isText(data) {
		return nil, 0, nil
	}
	lines := strings.Split(string(data), "\n")
	var matches []string
	count := 0
	for i, line := range lines {
		if count >= maxResults {
			break
		}
		if re.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%d:%s", i+1, strings.TrimRight(line, "\r")))
			count++
		}
	}
	return matches, count, nil
}
