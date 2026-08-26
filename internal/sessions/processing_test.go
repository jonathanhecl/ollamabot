package sessions

import (
	"context"
	"testing"
	"time"
)

func TestSessionCancelRegistry(t *testing.T) {
	const id = "sess-cancel"

	if AbortSession(id) {
		t.Fatal("expected false when nothing registered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	RegisterCancel(id, cancel)
	if !AbortSession(id) {
		t.Fatal("expected true when a cancel is registered")
	}
	if ctx.Err() == nil {
		t.Fatal("expected context to be cancelled")
	}
	if AbortSession(id) {
		t.Fatal("expected registry to be cleared after abort")
	}

	// Registering a new cancel replaces and cancels the previous one.
	prevCtx, prevCancel := context.WithCancel(context.Background())
	RegisterCancel(id, prevCancel)
	newCtx, newCancel := context.WithCancel(context.Background())
	RegisterCancel(id, newCancel)
	if prevCtx.Err() == nil {
		t.Fatal("expected previous cancel to be invoked on replace")
	}
	UnregisterCancel(id)
	if newCtx.Err() != nil {
		t.Fatal("expected unregister to not cancel the context")
	}
	if AbortSession(id) {
		t.Fatal("expected false after unregister")
	}
}

func TestPauseActivePlan(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := Session{ID: GenerateID(), Title: "Plan session", Model: "test"}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	if _, err := PauseActivePlan(store, sess.ID, "stop"); err == nil {
		t.Fatal("expected error when no active plan")
	}

	if _, err := ActivatePlan(store, sess.ID, "summary", []string{"one", "two"}); err != nil {
		t.Fatalf("activate plan: %v", err)
	}

	plan, err := PauseActivePlan(store, sess.ID, "Stopped by user")
	if err != nil {
		t.Fatalf("pause plan: %v", err)
	}
	if plan.Status != PlanStatusDeferred {
		t.Fatalf("expected deferred status, got %q", plan.Status)
	}
	if plan.DeferredUntil != nil {
		t.Fatalf("expected no scheduled resume, got %v", plan.DeferredUntil)
	}

	// Pausing again must fail (plan is no longer active).
	if _, err := PauseActivePlan(store, sess.ID, "stop"); err == nil {
		t.Fatal("expected error when pausing a non-active plan")
	}
}

func TestProcessingTracker(t *testing.T) {
	const id = "sess-1"

	if IsProcessing(id) {
		t.Fatal("expected idle before mark")
	}

	MarkProcessing(id)
	if !IsProcessing(id) {
		t.Fatal("expected processing after mark")
	}

	MarkProcessing(id)
	if !IsProcessing(id) {
		t.Fatal("expected processing with nested mark")
	}

	MarkIdle(id)
	if !IsProcessing(id) {
		t.Fatal("expected processing until all marks cleared")
	}

	MarkIdle(id)
	if IsProcessing(id) {
		t.Fatal("expected idle after all marks cleared")
	}
}

func TestTryMarkProcessing(t *testing.T) {
	const id = "sess-try"

	if IsProcessing(id) {
		t.Fatal("expected idle before try")
	}

	if !TryMarkProcessing(id) {
		t.Fatal("expected TryMarkProcessing to succeed on idle session")
	}

	if !IsProcessing(id) {
		t.Fatal("expected processing after successful TryMarkProcessing")
	}

	if TryMarkProcessing(id) {
		t.Fatal("expected TryMarkProcessing to fail on already-processing session")
	}

	MarkIdle(id)
	if IsProcessing(id) {
		t.Fatal("expected idle after MarkIdle")
	}

	if !TryMarkProcessing(id) {
		t.Fatal("expected TryMarkProcessing to succeed after MarkIdle")
	}
	MarkIdle(id)
}

func TestTryMarkProcessingBlockedByUserTurn(t *testing.T) {
	const id = "sess-user"

	MarkProcessing(id)
	if TryMarkProcessing(id) {
		t.Fatal("expected TryMarkProcessing to fail when user turn is active")
	}

	MarkIdle(id)
	if !TryMarkProcessing(id) {
		t.Fatal("expected TryMarkProcessing to succeed after user turn completes")
	}
	MarkIdle(id)
}

func TestTryAcquireBackgroundSlot(t *testing.T) {
	release := TryAcquireBackgroundSlot()
	if release == nil {
		t.Fatal("expected first TryAcquireBackgroundSlot to succeed")
	}

	if TryAcquireBackgroundSlot() != nil {
		t.Fatal("expected second TryAcquireBackgroundSlot to fail while first is held")
	}

	release()

	release2 := TryAcquireBackgroundSlot()
	if release2 == nil {
		t.Fatal("expected TryAcquireBackgroundSlot to succeed after release")
	}
	release2()
}

func TestInteractiveSlotBlocksBackgroundWork(t *testing.T) {
	release, err := AcquireInteractiveSlot(context.Background())
	if err != nil {
		t.Fatalf("AcquireInteractiveSlot: %v", err)
	}
	if TryAcquireBackgroundSlot() != nil {
		t.Fatal("expected background work to be blocked during an interactive turn")
	}
	release()

	backgroundRelease := TryAcquireBackgroundSlot()
	if backgroundRelease == nil {
		t.Fatal("expected background work to start after the interactive turn")
	}
	backgroundRelease()
}

func TestInteractiveSlotWaitsForBackgroundWork(t *testing.T) {
	backgroundRelease := TryAcquireBackgroundSlot()
	if backgroundRelease == nil {
		t.Fatal("expected background slot acquisition")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	acquired := make(chan func(), 1)
	go func() {
		release, err := AcquireInteractiveSlot(ctx)
		if err == nil {
			acquired <- release
		}
	}()

	select {
	case release := <-acquired:
		release()
		t.Fatal("interactive slot acquired while background work was active")
	case <-time.After(20 * time.Millisecond):
	}

	backgroundRelease()
	select {
	case release := <-acquired:
		release()
	case <-ctx.Done():
		t.Fatalf("interactive slot was not released: %v", ctx.Err())
	}
}
