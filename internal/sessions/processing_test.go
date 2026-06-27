package sessions

import "testing"

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
