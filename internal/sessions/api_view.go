package sessions

// SessionAPIView enriches a persisted session with ephemeral runtime fields.
type SessionAPIView struct {
	Session
	AgentBusy bool `json:"agent_busy"`
}

// SessionAPIViewFrom builds the API representation for a session.
func SessionAPIViewFrom(sess Session) SessionAPIView {
	return SessionAPIView{
		Session:   sess,
		AgentBusy: IsProcessing(sess.ID),
	}
}

// AgentStatusPayload is emitted over SSE when session activity changes.
type AgentStatusPayload struct {
	SessionID        string `json:"session_id"`
	Busy             bool   `json:"busy"`
	AwaitingApproval bool   `json:"awaiting_approval"`
}

// AgentStatusFor returns the current runtime status for a session.
func AgentStatusFor(sessionID string, sess Session) AgentStatusPayload {
	return AgentStatusPayload{
		SessionID:        sessionID,
		Busy:             IsProcessing(sessionID),
		AwaitingApproval: sess.PendingApproval != nil,
	}
}
