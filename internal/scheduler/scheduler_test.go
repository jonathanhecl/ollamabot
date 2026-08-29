package scheduler

import (
	"testing"
	"time"
)

func TestParseSchedule_RelativeDuration(t *testing.T) {
	ref := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)

	// "in 15m"
	schedType, next, expr, err := ParseSchedule("in 15m", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if schedType != ScheduleTypeOnce {
		t.Errorf("expected ScheduleTypeOnce, got %s", schedType)
	}
	expected := ref.Add(15 * time.Minute)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
	if expr != "" {
		t.Errorf("expected empty expr, got %q", expr)
	}

	// "2h"
	_, next, _, err = ParseSchedule("2h", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.Equal(ref.Add(2 * time.Hour)) {
		t.Errorf("expected 2h add, got %v", next)
	}

	// "1d"
	_, next, _, err = ParseSchedule("1d", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.Equal(ref.Add(24 * time.Hour)) {
		t.Errorf("expected 1d add, got %v", next)
	}
}

func TestParseSchedule_Interval(t *testing.T) {
	ref := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)

	schedType, next, expr, err := ParseSchedule("every 30m", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if schedType != ScheduleTypeInterval {
		t.Errorf("expected interval, got %v", schedType)
	}
	if !next.Equal(ref.Add(30 * time.Minute)) {
		t.Errorf("expected 30m next, got %v", next)
	}
	if expr != "30m" {
		t.Errorf("expected expr 30m, got %s", expr)
	}
}

func TestParseSchedule_Cron(t *testing.T) {
	ref := time.Date(2026, 8, 29, 8, 30, 0, 0, time.UTC)

	// "0 9 * * *" (Every day at 9:00 AM)
	schedType, next, expr, err := ParseSchedule("0 9 * * *", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if schedType != ScheduleTypeCron {
		t.Errorf("expected cron, got %v", schedType)
	}
	expected := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
	if expr != "0 9 * * *" {
		t.Errorf("expected expr, got %s", expr)
	}

	// "@daily"
	schedType, next, _, err = ParseSchedule("@daily", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if schedType != ScheduleTypeCron {
		t.Errorf("expected cron, got %v", schedType)
	}
	if !next.Equal(expected) {
		t.Errorf("expected daily at 9am (%v), got %v", expected, next)
	}
}

func TestNextCronTime_StepAndRange(t *testing.T) {
	ref := time.Date(2026, 8, 29, 10, 14, 0, 0, time.UTC)

	// "*/15 * * * *" -> should trigger at 10:15
	next, err := NextCronTime("*/15 * * * *", ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2026, 8, 29, 10, 15, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}
