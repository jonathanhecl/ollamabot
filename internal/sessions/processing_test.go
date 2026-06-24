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
