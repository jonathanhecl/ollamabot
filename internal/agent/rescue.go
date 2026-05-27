package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// pathMemory remembers absolute file paths the agent has observed via tool
// outputs so we can rescue later tool calls that arrive with a sloppy
// relative path or just a basename.
type pathMemory struct {
	mu     sync.Mutex
	cwd    string
	byName map[string]map[string]struct{}
}

func newPathMemory(cwd string) *pathMemory {
	abs, err := filepath.Abs(cwd)
	if err != nil || abs == "" {
		abs = cwd
	}
	return &pathMemory{
		cwd:    abs,
		byName: make(map[string]map[string]struct{}),
	}
}

// add stores an absolute path under its basename.
func (m *pathMemory) add(p string) {
	if p == "" || !filepath.IsAbs(p) {
		return
	}
	if _, err := os.Stat(p); err != nil {
		return
	}
	base := filepath.Base(p)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.byName[base]
	if !ok {
		set = make(map[string]struct{}, 2)
		m.byName[base] = set
	}
	set[p] = struct{}{}
}

// RememberToolResult mines a tool's output for absolute paths to index, and
// also indexes the resolved file_path of the call itself.
func (m *pathMemory) RememberToolResult(toolName string, params map[string]any, output string, isError bool) {
	if isError {
		return
	}
	if fp, ok := params["file_path"].(string); ok && fp != "" {
		if !filepath.IsAbs(fp) {
			abs := filepath.Join(m.cwd, fp)
			if absAbs, err := filepath.Abs(abs); err == nil {
				m.add(absAbs)
			}
		} else {
			m.add(fp)
		}
	}
	if fp, ok := params["path"].(string); ok && fp != "" {
		if !filepath.IsAbs(fp) {
			abs := filepath.Join(m.cwd, fp)
			if absAbs, err := filepath.Abs(abs); err == nil {
				m.add(absAbs)
			}
		} else {
			m.add(fp)
		}
	}

	for _, line := range strings.Split(output, "\n") {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		// Grep emits "path:line:match"; take the first colon-separated
		// chunk that looks like an absolute path.
		if !filepath.IsAbs(token) {
			if i := strings.Index(token, ":"); i > 0 {
				head := token[:i]
				if len(head) == 1 && i+1 < len(token) {
					if j := strings.Index(token[i+1:], ":"); j > 0 {
						head = token[:i+1+j]
					}
				}
				if filepath.IsAbs(head) {
					token = head
				}
			}
		}
		m.add(token)
	}
}

// Resolve maps a path to an absolute path that exists.
func (m *pathMemory) Resolve(p string) (abs string, rescued bool, ok bool) {
	if strings.TrimSpace(p) == "" {
		return "", false, false
	}
	p = strings.TrimSpace(p)
	if filepath.IsAbs(p) {
		if _, err := os.Stat(p); err == nil {
			return p, false, true
		}
	} else {
		joined := filepath.Join(m.cwd, p)
		if _, err := os.Stat(joined); err == nil {
			return joined, false, true
		}
	}
	if c, ok := m.rescueUniqueBasename(p); ok {
		return c, true, true
	}
	if filepath.IsAbs(p) {
		return p, false, false
	}
	return "", false, false
}

// rescueUniqueBasename returns the only indexed absolute path for filepath.Base(p).
func (m *pathMemory) rescueUniqueBasename(p string) (string, bool) {
	base := filepath.Base(p)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := m.byName[base]
	if len(candidates) != 1 {
		return "", false
	}
	for c := range candidates {
		return c, true
	}
	return "", false
}

func pathParamKeyForTool(toolName string) string {
	switch toolName {
	case "read_file", "Write", "Edit":
		return "file_path"
	case "path":
		return "path"
	}
	return ""
}
