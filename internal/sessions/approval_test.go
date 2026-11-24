package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
)

func TestApprovalServiceClearsPendingOnCancelAndDeletesRemember(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := Session{ID: "approval-cancel-sess", Title: "Approval Test"}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	svc := NewApprovalService(store, config.NewManager(config.Config{}))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()

	approved, err := svc.RequestApprovalWithRisk(ctx, sess.ID, "execute_command", map[string]any{"command": "echo hi"}, "runs shell")
	if approved {
		t.Fatal("expected approved=false on cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	reloaded, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if reloaded.PendingApproval != nil {
		t.Fatalf("expected PendingApproval cleared on cancellation, got %+v", reloaded.PendingApproval)
	}

	svc.mu.Lock()
	remLen := len(svc.remember)
	svc.mu.Unlock()
	if remLen != 0 {
		t.Fatalf("expected remember map empty, got %d entries", remLen)
	}
}
