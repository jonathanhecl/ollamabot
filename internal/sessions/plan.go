package sessions

import (
	"fmt"
	"strings"
)

const (
	PlanStatusActive    = "active"
	PlanStatusCompleted = "completed"
	PlanStatusRejected  = "rejected"
)

// SessionPlan stores a user-approved execution plan and its visible progress.
type SessionPlan struct {
	Summary   string   `json:"summary"`
	Steps     []string `json:"steps"`
	Completed int      `json:"completed"`
	Status    string   `json:"status"` // active | completed | rejected
}

func cloneSessionPlan(plan *SessionPlan) *SessionPlan {
	if plan == nil {
		return nil
	}
	cloned := *plan
	if plan.Steps != nil {
		cloned.Steps = append([]string(nil), plan.Steps...)
	}
	return &cloned
}

// ActivatePlan stores a newly approved plan on a session.
func ActivatePlan(store *Store, sessionID string, summary string, steps []string) (SessionPlan, error) {
	if store == nil {
		return SessionPlan{}, fmt.Errorf("session store is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionPlan{}, fmt.Errorf("session ID is required")
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return SessionPlan{}, fmt.Errorf("plan summary is required")
	}
	cleanSteps := cleanPlanSteps(steps)
	if len(cleanSteps) == 0 {
		return SessionPlan{}, fmt.Errorf("plan requires at least 1 step")
	}

	sess, err := store.Get(sessionID)
	if err != nil {
		return SessionPlan{}, err
	}
	plan := SessionPlan{
		Summary:   summary,
		Steps:     cleanSteps,
		Completed: 0,
		Status:    PlanStatusActive,
	}
	sess.ActivePlan = &plan
	if err := store.Save(sess); err != nil {
		return SessionPlan{}, err
	}
	NotifyUpdate(sessionID)
	return plan, nil
}

// CompletePlanStep advances the active plan by exactly one completed top-level step.
func CompletePlanStep(store *Store, sessionID string, note string) (SessionPlan, string, error) {
	if store == nil {
		return SessionPlan{}, "", fmt.Errorf("session store is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionPlan{}, "", fmt.Errorf("session ID is required")
	}
	sess, err := store.Get(sessionID)
	if err != nil {
		return SessionPlan{}, "", err
	}
	if sess.ActivePlan == nil || sess.ActivePlan.Status != PlanStatusActive {
		return SessionPlan{}, "", fmt.Errorf("no active plan for this session")
	}
	plan := cloneSessionPlan(sess.ActivePlan)
	if plan.Completed < len(plan.Steps) {
		plan.Completed++
	}
	var message string
	if plan.Completed >= len(plan.Steps) {
		plan.Completed = len(plan.Steps)
		plan.Status = PlanStatusCompleted
		message = "All plan steps completed."
	} else {
		next := plan.Steps[plan.Completed]
		message = fmt.Sprintf("Step %d completed. Next: %s", plan.Completed, next)
	}
	if strings.TrimSpace(note) != "" {
		message += " Note: " + strings.TrimSpace(note)
	}

	sess.ActivePlan = plan
	if err := store.Save(sess); err != nil {
		return SessionPlan{}, "", err
	}
	NotifyUpdate(sessionID)
	return *plan, message, nil
}

// ClearActivePlan removes the current plan from a session, usually after rejection.
func ClearActivePlan(store *Store, sessionID string) error {
	if store == nil {
		return fmt.Errorf("session store is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	sess, err := store.Get(sessionID)
	if err != nil {
		return err
	}
	sess.ActivePlan = nil
	if err := store.Save(sess); err != nil {
		return err
	}
	NotifyUpdate(sessionID)
	return nil
}

func cleanPlanSteps(steps []string) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		step = strings.TrimSpace(step)
		if step != "" {
			out = append(out, step)
		}
	}
	return out
}
