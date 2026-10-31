package device

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// deviceLinksFile stores the device → session mapping inside the sessions
// directory so a device keeps one conversation across restarts.
const deviceLinksFile = "devices.json"

// sessionLinks persists which conversation belongs to each physical device.
// A device survives process restarts and the web UI edits the same sessions.
type sessionLinks struct {
	mu       sync.Mutex
	path     string
	byDevice map[string]string
}

func loadSessionLinks(path string) *sessionLinks {
	links := &sessionLinks{path: path, byDevice: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return links
	}
	if err := json.Unmarshal(data, &links.byDevice); err != nil {
		return links
	}
	if links.byDevice == nil {
		links.byDevice = map[string]string{}
	}
	return links
}

// Get returns the linked session ID, or empty when the device never talked.
func (l *sessionLinks) Get(deviceID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byDevice[deviceID]
}

// Set links a device to a session and persists the mapping atomically.
func (l *sessionLinks) Set(deviceID, sessionID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byDevice == nil {
		l.byDevice = map[string]string{}
	}
	l.byDevice[deviceID] = sessionID
	return l.saveLocked()
}

func (l *sessionLinks) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l.byDevice, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
