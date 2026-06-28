package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListFiles lists files and directories at the given path within the workspace.
// When recursive is true, walks all subdirectories. When includeGlob is set,
// only files matching the glob pattern are returned.
func ListFiles(workspace, searchPath string, recursive bool, includeGlob string) (string, error) {
	root, err := ResolveAndValidatePath(workspace, searchPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("path not found: %w", err)
	}
	if !info.IsDir() {
		return fmt.Sprintf("%s  (%d bytes)", searchPath, info.Size()), nil
	}

	type entry struct {
		relPath string
		isDir   bool
		size    int64
	}
	var entries []entry

	if recursive {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				if path == root {
					return nil
				}
				entries = append(entries, entry{relPath: relPath(workspace, path), isDir: true})
				return nil
			}
			if includeGlob != "" {
				matched, _ := filepath.Match(includeGlob, d.Name())
				if !matched {
					return nil
				}
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, entry{relPath: relPath(workspace, path), isDir: false, size: info.Size()})
			return nil
		})
	} else {
		dirEntries, err2 := os.ReadDir(root)
		if err2 != nil {
			return "", err2
		}
		for _, d := range dirEntries {
			if d.IsDir() {
				entries = append(entries, entry{relPath: relPath(workspace, filepath.Join(root, d.Name())), isDir: true})
				continue
			}
			if includeGlob != "" {
				matched, _ := filepath.Match(includeGlob, d.Name())
				if !matched {
					continue
				}
			}
			info, _ := d.Info()
			entries = append(entries, entry{relPath: relPath(workspace, filepath.Join(root, d.Name())), isDir: false, size: info.Size()})
		}
	}

	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "Empty directory.", nil
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isDir != entries[j].isDir {
			return entries[i].isDir
		}
		return entries[i].relPath < entries[j].relPath
	})

	var sb strings.Builder
	for _, e := range entries {
		if e.isDir {
			sb.WriteString(fmt.Sprintf("  %s/\n", e.relPath))
		} else {
			sb.WriteString(fmt.Sprintf("  %s  (%d bytes)\n", e.relPath, e.size))
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func relPath(workspace, abs string) string {
	rel, err := filepath.Rel(workspace, abs)
	if err != nil {
		return abs
	}
	return rel
}
