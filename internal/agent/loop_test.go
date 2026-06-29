package agent

import (
	"context"
	"fmt"
	"strings"
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

func TestTimeContextMessageFormat(t *testing.T) {
	now := time.Now()
	_, offset := now.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	utcOffset := fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
	timeStr := now.Format("Monday, January 2, 2006 at 3:04 PM")
	content := fmt.Sprintf("Current date and time: %s (%s)", timeStr, utcOffset)

	if !strings.HasPrefix(content, "Current date and time: ") {
		t.Fatalf("expected prefix 'Current date and time: ', got %q", content)
	}
	if !strings.Contains(content, "UTC") {
		t.Fatalf("expected UTC offset in message, got %q", content)
	}
	if !strings.Contains(content, now.Format("2006")) {
		t.Fatalf("expected current year in message, got %q", content)
	}
}
