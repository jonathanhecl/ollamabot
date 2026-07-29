package sessions

import (
	"context"
	"strings"
	"sync"
)

var (
	processingTrackerMu sync.RWMutex
	processingTracker   = newSessionActivityTracker()

	backgroundWorkMu   sync.Mutex
	backgroundWorkBusy bool

	sessionCancelsMu sync.Mutex
	sessionCancels   = make(map[string]context.CancelFunc)
)

// RegisterCancel registers a cancel function for the in-flight work of a
// session (user turn, plan monitor run, goal cycle). Any previously
// registered cancel is invoked and replaced.
func RegisterCancel(sessionID string, cancel context.CancelFunc) {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" || cancel == nil {
		return
	}
	sessionCancelsMu.Lock()
	if old, ok := sessionCancels[sessionID]; ok {
		old()
	}
	sessionCancels[sessionID] = cancel
	sessionCancelsMu.Unlock()
}

// UnregisterCancel removes the registered cancel for a session, if any.
func UnregisterCancel(sessionID string) {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" {
		return
	}
	sessionCancelsMu.Lock()
	delete(sessionCancels, sessionID)
	sessionCancelsMu.Unlock()
}

// AbortSession cancels the registered in-flight work for a session.
// Returns true if there was something to cancel.
func AbortSession(sessionID string) bool {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" {
		return false
	}
	sessionCancelsMu.Lock()
	cancel, ok := sessionCancels[sessionID]
	delete(sessionCancels, sessionID)
	sessionCancelsMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

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

// TryMarkProcessing atomically checks if the session is idle and marks it as processing.
// Returns true if the session was idle and is now marked, false if already processing.
// User-initiated turns should use MarkProcessing (which always proceeds).
// Background managers should use TryMarkProcessing to avoid concurrent loops on the same session.
func TryMarkProcessing(sessionID string) bool {
	sessionID = normalizeSessionID(sessionID)
	if sessionID == "" {
		return false
	}
	processingTrackerMu.Lock()
	defer processingTrackerMu.Unlock()
	if processingTracker.counts[sessionID] > 0 {
		return false
	}
	processingTracker.counts[sessionID] = 1
	return true
}

// TryAcquireBackgroundSlot attempts to claim the single global background agent slot.
// Returns a release function if successful, nil if another background loop is running.
// This ensures only one background agent loop runs at a time across all managers
// (GoalManager, PlanMonitor, SleepManager), preventing Ollama overload.
// User turns (engine.ProcessTurn) are not affected.
func TryAcquireBackgroundSlot() func() {
	backgroundWorkMu.Lock()
	if backgroundWorkBusy {
		backgroundWorkMu.Unlock()
		return nil
	}
	backgroundWorkBusy = true
	backgroundWorkMu.Unlock()
	return func() {
		backgroundWorkMu.Lock()
		backgroundWorkBusy = false
		backgroundWorkMu.Unlock()
	}
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
