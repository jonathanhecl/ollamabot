package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
)

const ApprovalTimeout = 5 * time.Minute

// PendingApproval is the persisted reason a session is paused for a security decision.
type PendingApproval struct {
	ID          string         `json:"id"`
	Tool        string         `json:"tool"`
	Arguments   map[string]any `json:"arguments"`
	Signature   string         `json:"signature"`
	Label       string         `json:"label"`
	RiskSummary string         `json:"risk_summary,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
}

// SessionApprovalGrant lets a previously approved tool invocation run again in the same session.
type SessionApprovalGrant struct {
	Tool      string    `json:"tool"`
	Signature string    `json:"signature"`
	Label     string    `json:"label"`
	GrantedAt time.Time `json:"granted_at"`
}

// ApprovalDecision is the user's answer to a pending approval.
type ApprovalDecision struct {
	Approved           bool
	RememberForSession bool
}

type ApprovalNotifier func(sessionID string, approval PendingApproval)

type pendingApprovalRef struct {
	sessionID string
	approval  PendingApproval
}

// ApprovalService persists pending approvals and coordinates channel responses.
type ApprovalService struct {
	store  *Store
	cfgMgr *config.Manager

	mu        sync.Mutex
	waiters   map[string]chan ApprovalDecision
	pending   map[string]pendingApprovalRef
	remember  map[string]bool
	notifiers map[string]map[int]ApprovalNotifier
	nextID    int
}

func (s *ApprovalService) workspace() string {
	if s.cfgMgr == nil {
		return ""
	}
	return s.cfgMgr.Get().Workspace
}

func NewApprovalService(store *Store, cfg *config.Manager) *ApprovalService {
	return &ApprovalService{
		store:     store,
		cfgMgr:    cfg,
		waiters:   make(map[string]chan ApprovalDecision),
		pending:   make(map[string]pendingApprovalRef),
		remember:  make(map[string]bool),
		notifiers: make(map[string]map[int]ApprovalNotifier),
	}
}

func (s *ApprovalService) RegisterNotifier(sessionID string, notifier ApprovalNotifier) func() {
	if s == nil || strings.TrimSpace(sessionID) == "" || notifier == nil {
		return func() {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	if s.notifiers[sessionID] == nil {
		s.notifiers[sessionID] = make(map[int]ApprovalNotifier)
	}
	s.notifiers[sessionID][id] = notifier
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.notifiers[sessionID] != nil {
			delete(s.notifiers[sessionID], id)
			if len(s.notifiers[sessionID]) == 0 {
				delete(s.notifiers, sessionID)
			}
		}
	}
}

func (s *ApprovalService) HasGrant(sessionID, tool string, args map[string]any) bool {
	if s == nil || s.store == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	signature, _ := FormatApprovalSignature(tool, args, s.workspace())
	sess, err := s.store.Get(sessionID)
	if err != nil {
		return false
	}
	for _, grant := range sess.ApprovalGrants {
		if grant.Tool == tool && grant.Signature == signature {
			return true
		}
	}
	return false
}

func (s *ApprovalService) RequestApproval(ctx context.Context, sessionID, tool string, args map[string]any) (bool, error) {
	return s.RequestApprovalWithRisk(ctx, sessionID, tool, args, "")
}

func (s *ApprovalService) RequestApprovalWithRisk(ctx context.Context, sessionID, tool string, args map[string]any, riskSummary string) (bool, error) {
	if s == nil || s.store == nil || strings.TrimSpace(sessionID) == "" {
		return false, fmt.Errorf("approval service is not configured")
	}
	if s.HasGrant(sessionID, tool, args) {
		return true, nil
	}

	signature, label := FormatApprovalSignature(tool, args, s.workspace())
	now := time.Now()
	approval := PendingApproval{
		ID:          fmt.Sprintf("approval_%d_%s", now.UnixNano(), safeApprovalIDPart(tool)),
		Tool:        tool,
		Arguments:   cloneApprovalArgs(args),
		Signature:   signature,
		Label:       label,
		RiskSummary: strings.TrimSpace(riskSummary),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ApprovalTimeout),
	}
	waiter := make(chan ApprovalDecision, 1)

	s.mu.Lock()
	s.waiters[approval.ID] = waiter
	s.pending[approval.ID] = pendingApprovalRef{sessionID: sessionID, approval: approval}
	s.mu.Unlock()

	if err := s.savePending(sessionID, &approval); err != nil {
		s.mu.Lock()
		delete(s.waiters, approval.ID)
		delete(s.pending, approval.ID)
		delete(s.remember, approval.ID)
		s.mu.Unlock()
		return false, err
	}
	NotifyUpdate(sessionID)
	s.notify(sessionID, approval)

	timeout := time.NewTimer(ApprovalTimeout)
	defer timeout.Stop()
	defer func() {
		s.mu.Lock()
		delete(s.waiters, approval.ID)
		delete(s.pending, approval.ID)
		s.mu.Unlock()
	}()

	select {
	case decision := <-waiter:
		s.mu.Lock()
		if s.remember[approval.ID] {
			decision.RememberForSession = true
		}
		s.mu.Unlock()
		if err := s.resolvePending(sessionID, approval, decision); err != nil {
			return false, err
		}
		return decision.Approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timeout.C:
		_ = s.resolvePending(sessionID, approval, ApprovalDecision{Approved: false})
		return false, fmt.Errorf("approval timeout")
	}
}

func (s *ApprovalService) RespondApproval(id string, decision ApprovalDecision) error {
	if s == nil || strings.TrimSpace(id) == "" {
		return fmt.Errorf("missing approval id")
	}
	s.mu.Lock()
	if decision.RememberForSession {
		s.remember[id] = true
	}
	waiter := s.waiters[id]
	ref, hasRef := s.pending[id]
	s.mu.Unlock()
	if waiter != nil {
		select {
		case waiter <- decision:
			if hasRef {
				_ = s.resolvePending(ref.sessionID, ref.approval, decision)
			}
			return nil
		default:
			return fmt.Errorf("approval request already answered")
		}
	}
	sessionID, approval, err := s.findPendingByID(id)
	if err != nil {
		return err
	}
	return s.resolvePending(sessionID, approval, decision)
}

func (s *ApprovalService) savePending(sessionID string, approval *PendingApproval) error {
	sess, err := s.store.Get(sessionID)
	if err != nil {
		return err
	}
	sess.PendingApproval = approval
	return s.store.Save(sess)
}

func (s *ApprovalService) resolvePending(sessionID string, approval PendingApproval, decision ApprovalDecision) error {
	sess, err := s.store.Get(sessionID)
	if err != nil {
		return err
	}
	if sess.PendingApproval != nil && sess.PendingApproval.ID == approval.ID {
		sess.PendingApproval = nil
	}
	if decision.Approved && decision.RememberForSession {
		hasGrant := false
		for _, grant := range sess.ApprovalGrants {
			if grant.Tool == approval.Tool && grant.Signature == approval.Signature {
				hasGrant = true
				break
			}
		}
		if !hasGrant {
			sess.ApprovalGrants = append(sess.ApprovalGrants, SessionApprovalGrant{
				Tool:      approval.Tool,
				Signature: approval.Signature,
				Label:     approval.Label,
				GrantedAt: time.Now(),
			})
		}
	}
	if err := s.store.Save(sess); err != nil {
		return err
	}
	NotifyUpdate(sessionID)
	return nil
}

func (s *ApprovalService) findPendingByID(id string) (string, PendingApproval, error) {
	if s.store == nil {
		return "", PendingApproval{}, fmt.Errorf("approval service is not configured")
	}
	sessions, err := s.store.List()
	if err != nil {
		return "", PendingApproval{}, err
	}
	for _, entry := range sessions {
		sess, err := s.store.Get(entry.ID)
		if err != nil {
			continue
		}
		if sess.PendingApproval != nil && sess.PendingApproval.ID == id {
			return sess.ID, *sess.PendingApproval, nil
		}
	}
	return "", PendingApproval{}, fmt.Errorf("approval request not found or expired")
}

func (s *ApprovalService) notify(sessionID string, approval PendingApproval) {
	s.mu.Lock()
	notifiers := make([]ApprovalNotifier, 0, len(s.notifiers[sessionID]))
	for _, notifier := range s.notifiers[sessionID] {
		notifiers = append(notifiers, notifier)
	}
	s.mu.Unlock()
	for _, notifier := range notifiers {
		notifier(sessionID, approval)
	}
}

func FormatApprovalSignature(tool string, args map[string]any, workspace string) (signature string, label string) {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "execute_command":
		command, _ := args["command"].(string)
		command = filepath.Base(strings.TrimSpace(command))
		argv := normalizeCommandArgs(command, stringSlice(args["args"]), workspace)
		parts := append([]string{command}, argv...)
		label = strings.Join(parts, " ")
		return "execute_command:" + strings.Join(parts, "\x00"), label
	case "write_file", "edit_file":
		filePath, _ := args["file_path"].(string)
		abs := normalizeApprovalPath(filePath, workspace)
		label = fmt.Sprintf("%s %s", tool, abs)
		return tool + ":" + abs, label
	default:
		b, _ := json.Marshal(args)
		label = tool
		if len(b) > 0 {
			label += " " + string(b)
		}
		return tool + ":" + string(b), label
	}
}

func NormalizeExecuteCommandArgs(command string, args []string, workspace string) []string {
	return normalizeCommandArgs(filepath.Base(strings.TrimSpace(command)), args, workspace)
}

func normalizeCommandArgs(command string, args []string, workspace string) []string {
	out := make([]string, 0, len(args))
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if i == 0 && command != "" && filepath.Base(arg) == command {
			continue
		}
		out = append(out, normalizeMaybePath(arg, workspace))
	}
	return out
}

func stringSlice(v any) []string {
	raw, ok := v.([]any)
	if ok {
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	if ss, ok := v.([]string); ok {
		return append([]string(nil), ss...)
	}
	return nil
}

func normalizeMaybePath(arg string, workspace string) string {
	if strings.HasPrefix(arg, "-") || workspace == "" {
		return arg
	}
	if filepath.IsAbs(arg) {
		return filepath.Clean(arg)
	}
	if strings.Contains(arg, string(filepath.Separator)) || strings.Contains(filepath.Base(arg), ".") {
		return filepath.Clean(filepath.Join(workspace, arg))
	}
	return arg
}

func normalizeApprovalPath(path string, workspace string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workspace, path))
}

func safeApprovalIDPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
	if s == "" {
		return "tool"
	}
	return s
}

func cloneApprovalArgs(args map[string]any) map[string]any {
	b, err := json.Marshal(args)
	if err != nil {
		out := make(map[string]any, len(args))
		for k, v := range args {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}
