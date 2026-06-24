package sessions

// IsAutonomousSession reports whether a session has an approved execution
// contract that should continue without interrupting for routine safe commands.
func IsAutonomousSession(sess Session) bool {
	if sess.ActivePlan != nil &&
		sess.ActivePlan.Status == PlanStatusActive &&
		sess.ActivePlan.Completed < len(sess.ActivePlan.Steps) {
		return true
	}
	return sess.GoalStatus == "active"
}
