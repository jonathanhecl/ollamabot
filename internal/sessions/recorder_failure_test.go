package sessions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRecordFailureLeavesTraceInSession guards the review path: an aborted turn
// must leave the reason in the timeline, because the web is where a session is
// inspected after the fact.
func TestRecordFailureLeavesTraceInSession(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(Session{ID: "sess-fail", Title: "fail", Model: "main"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	rec := NewRecorder(store, "sess-fail", []RawMsg{{Role: "user", Content: "clima en Buenos Aires"}}, "main", "device")

	rec.RecordFailure(errTest("detected repetitive loop: send_files called 2 times"))
	if _, err := rec.SnapshotAndSave(); err != nil {
		t.Fatalf("SnapshotAndSave: %v", err)
	}

	sess, err := store.Get("sess-fail")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session has %d messages, want user + assistant", len(sess.Messages))
	}
	var assistant RawMsg
	if err := json.Unmarshal(sess.Messages[1], &assistant); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assistant.Content, "detected repetitive loop") {
		t.Fatalf("assistant content = %q, want the failure reason", assistant.Content)
	}
	var found bool
	for _, step := range assistant.Steps {
		if step.Type == "error" && strings.Contains(step.Content, "detected repetitive loop") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no error step recorded: %+v", assistant.Steps)
	}
}

func TestRecordFailureKeepsModelText(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(Session{ID: "sess-fail2", Title: "fail", Model: "main"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	rec := NewRecorder(store, "sess-fail2", nil, "main", "device")

	rec.OnContent("Estaba buscando el clima")
	rec.RecordFailure(context.Canceled)
	if _, err := rec.SnapshotAndSave(); err != nil {
		t.Fatalf("SnapshotAndSave: %v", err)
	}

	sess, err := store.Get("sess-fail2")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	var assistant RawMsg
	if err := json.Unmarshal(sess.Messages[len(sess.Messages)-1], &assistant); err != nil {
		t.Fatal(err)
	}
	if assistant.Content != "Estaba buscando el clima" {
		t.Fatalf("partial model text was overwritten: %q", assistant.Content)
	}
	if len(assistant.Steps) == 0 || assistant.Steps[len(assistant.Steps)-1].Content != "Turn canceled." {
		t.Fatalf("canceled step = %+v", assistant.Steps)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
