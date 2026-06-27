package agent

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
)

func TestSubagentContextExpiresAfterConfiguredDuration(t *testing.T) {
	cfg := config.Config{SubagentTimeoutMinutes: 1}
	ctx, cancel := SubagentContext(context.Background(), cfg)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected context to have a deadline")
	}
	expected := time.Now().Add(1 * time.Minute)
	tolerance := 5 * time.Second
	if deadline.Before(expected.Add(-tolerance)) || deadline.After(expected.Add(tolerance)) {
		t.Fatalf("deadline %v not within tolerance of expected %v", deadline, expected)
	}
}

func TestSubagentContextDefaultsTo10Minutes(t *testing.T) {
	cfg := config.Config{SubagentTimeoutMinutes: 0}
	ctx, cancel := SubagentContext(context.Background(), cfg)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected context to have a deadline")
	}
	expected := time.Now().Add(10 * time.Minute)
	tolerance := 5 * time.Second
	if deadline.Before(expected.Add(-tolerance)) || deadline.After(expected.Add(tolerance)) {
		t.Fatalf("deadline %v not within tolerance of expected %v (default 10m)", deadline, expected)
	}
}

func TestSubagentContextCancelPreventsTimeout(t *testing.T) {
	cfg := config.Config{SubagentTimeoutMinutes: 1}
	ctx, cancel := SubagentContext(context.Background(), cfg)
	cancel()

	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", ctx.Err())
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("context was not cancelled after calling cancel()")
	}
}
