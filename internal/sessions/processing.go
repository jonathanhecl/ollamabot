package sessions

import (
	"strings"
	"sync"
)

var (
	processingTrackerMu sync.RWMutex
	processingTracker   = newSessionActivityTracker()
)

type sessionActivityTracker struct {
	counts map[string]int
}

func newSessionActivityTracker() *sessionActivityTracker {
	return &sessionActivityTracker{counts: make(map[string]int)}
}

func normalizeSessionID(sessionID string) string {
	return strings.TrimSpace(sessionID)
}

// MarkProcessing marks a session as actively running an agent turn.
func MarkProcessing(sessionID string) {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" {
		return
	}
	processingTracker.mark(sessionID)
	NotifyUpdate(sessionID)
}

// MarkIdle clears one active agent turn for the session.
func MarkIdle(sessionID string) {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" {
		return
	}
	processingTracker.unmark(sessionID)
	NotifyUpdate(sessionID)
}

// IsProcessing reports whether any agent turn is currently running for the session.
func IsProcessing(sessionID string) bool {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" {
		return false
	}
	return processingTracker.isActive(sessionID)
}

func (t *sessionActivityTracker) mark(sessionID string) {
	processingTrackerMu.Lock()
	defer processingTrackerMu.Unlock()
	t.counts[sessionID]++
}

func (t *sessionActivityTracker) unmark(sessionID string) {
	processingTrackerMu.Lock()
	defer processingTrackerMu.Unlock()
	if t.counts[sessionID] <= 1 {
		delete(t.counts, sessionID)
		return
	}
	t.counts[sessionID]--
}

func (t *sessionActivityTracker) isActive(sessionID string) bool {
	processingTrackerMu.RLock()
	defer processingTrackerMu.RUnlock()
	return t.counts[sessionID] > 0
}
