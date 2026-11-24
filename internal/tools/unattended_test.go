package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func unattendedToolCall(name string, args map[string]any) ollama.ToolCall {
	raw, _ := json.Marshal(args)
	return ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      name,
			Arguments: raw,
		},
	}
}

// An unattended run (scheduled task, background loop, plan resume) has nobody
// to answer. ask_clarification would fail and present_plan would be approved by
// the run itself, leaving an active plan no one reviews; the plan monitor then
// retries it forever. Neither tool may reach the model.
func TestUnattendedRunsHideInteractiveTools(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetChannel("scheduler")
	registry.SetUnattended(true)

	for _, name := range []string{"ask_clarification", "present_plan"} {
		if hasTool(registry, name) {
			t.Fatalf("%s should not be offered in an unattended run", name)
		}
	}

	// A stale definition must not be able to block the turn either.
	_, err := registry.Execute(context.Background(), unattendedToolCall("present_plan", map[string]any{
		"summary": "Configure the task",
		"steps":   []string{"Ask the user for the hour"},
	}))
	if err == nil || !strings.Contains(err.Error(), "unattended run") {
		t.Fatalf("expected present_plan to be rejected in an unattended run, got %v", err)
	}
	_, err = registry.Execute(context.Background(), unattendedToolCall("ask_clarification", map[string]any{
		"question": "Which hour?",
	}))
	if err == nil || !strings.Contains(err.Error(), "unattended run") {
		t.Fatalf("expected ask_clarification to be rejected in an unattended run, got %v", err)
	}

	// Tools that a run really needs stay available.
	for _, name := range []string{"todo_write", "read_file", "complete_plan_step"} {
		if !hasTool(registry, name) {
			t.Fatalf("%s should stay available in an unattended run", name)
		}
	}
}

// An interactive turn keeps the whole set: the same registry is rebuilt per
// turn, and only unattended runs lose the tools.
func TestInteractiveRunsKeepInteractiveTools(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetChannel("web")

	for _, name := range []string{"ask_clarification", "present_plan"} {
		if !hasTool(registry, name) {
			t.Fatalf("%s should be offered in an interactive turn", name)
		}
	}
}

func TestSchedulerChannelHidesAndRejectsContradictoryTools(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSchedulerService(&mockSchedulerService{})
	registry.SetSpeechModel("test-speech")
	registry.SetTelegramConfig("bot-token", []string{"123"})
	registry.SetPlanConfirmMode("always")
	registry.SetChannel("scheduler")
	registry.SetUnattended(true)

	if registry.VoiceEnabled() {
		t.Fatal("VoiceEnabled must be false on scheduler channel")
	}

	forbidden := []string{
		"ask_clarification", "present_plan", "defer_plan_continuation",
		"send_telegram", "send_voice", "send_files",
		"schedule_task", "schedule_reminder", "schedule_list",
		"schedule_update", "schedule_delete", "schedule_cancel", "schedule_run",
	}
	for _, name := range forbidden {
		if hasTool(registry, name) {
			t.Fatalf("%s should not be offered on scheduler channel", name)
		}
		_, err := registry.Execute(context.Background(), unattendedToolCall(name, map[string]any{}))
		if err == nil {
			t.Fatalf("expected %s execution to be rejected on scheduler channel", name)
		}
	}
}

func TestUnattendedHidesAndRejectsVoice(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetSpeechModel("test-speech")
	registry.SetChannel("web")
	registry.SetUnattended(true)

	if registry.VoiceEnabled() {
		t.Fatal("VoiceEnabled must be false in unattended runs")
	}
	if hasTool(registry, "send_voice") {
		t.Fatal("send_voice should not be offered in unattended runs")
	}
	_, err := registry.Execute(context.Background(), unattendedToolCall("send_voice", map[string]any{"text": "hi"}))
	if err == nil || !strings.Contains(err.Error(), "unattended run") {
		t.Fatalf("expected send_voice to be rejected in unattended run, got %v", err)
	}
}

func TestUnattendedEnforcesRiskGateWithoutHandler(t *testing.T) {
	ws := t.TempDir()
	registry := NewRegistry(false, ws, nil, nil, "", SearchConfig{})
	registry.SetApprovalPolicy(ApprovalPolicyAutonomous)
	registry.SetChannel("autonomous")
	registry.SetUnattended(true)

	// RiskNeedsApproval (git push) must be blocked when no approval handler/service exists.
	_, err := registry.Execute(context.Background(), unattendedToolCall("execute_command", map[string]any{
		"command": "git",
		"args":    []any{"push"},
	}))
	if err == nil || !strings.Contains(err.Error(), "requires approval") {
		t.Fatalf("expected risky command without approval handler to be blocked, got %v", err)
	}

	// RiskBlocked (disallowed executable) must also be blocked.
	_, err = registry.Execute(context.Background(), unattendedToolCall("execute_command", map[string]any{
		"command": "bash",
		"args":    []any{"-c", "echo hi"},
	}))
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected disallowed command to be blocked, got %v", err)
	}

	// RiskSafe workspace write still runs cleanly without interactive approval.
	out, err := registry.Execute(context.Background(), unattendedToolCall("write_file", map[string]any{
		"file_path": "ok.txt",
		"contents":  "safe write",
	}))
	if err != nil {
		t.Fatalf("expected safe workspace write to succeed, got %v", err)
	}
	if !strings.Contains(out, "Write successful") {
		t.Fatalf("unexpected write_file output: %q", out)
	}
	if raw, readErr := os.ReadFile(filepath.Join(ws, "ok.txt")); readErr != nil || string(raw) != "safe write" {
		t.Fatalf("expected workspace file written, got err=%v content=%q", readErr, string(raw))
	}
}

func TestApprovalWaitHooksWrapRequestApproval(t *testing.T) {
	ws := t.TempDir()
	store := sessions.NewStore(t.TempDir())
	sess := sessions.Session{ID: "hook-sess", Title: "Hook Test"}
	if err := store.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	cfgMgr := config.NewManager(config.Config{Workspace: ws})
	svc := sessions.NewApprovalService(store, cfgMgr)

	events := make(chan string, 4)
	svc.RegisterNotifier(sess.ID, func(_ string, approval sessions.PendingApproval) {
		events <- "notify"
		_ = svc.RespondApproval(approval.ID, sessions.ApprovalDecision{Approved: false})
	})

	registry := NewRegistry(false, ws, nil, nil, "", SearchConfig{})
	registry.SetSessionID(sess.ID)
	registry.SetApprovalService(svc)
	registry.SetApprovalWaitHooks(
		func() { events <- "wait_start" },
		func(context.Context) error { events <- "wait_end"; return nil },
	)

	res, err := registry.Execute(context.Background(), unattendedToolCall("execute_command", map[string]any{
		"command": "git",
		"args":    []any{"push"},
	}))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res, "denied by user") {
		t.Fatalf("expected denial message, got %q", res)
	}

	close(events)
	var order []string
	for ev := range events {
		order = append(order, ev)
	}
	got := strings.Join(order, ",")
	if got != "wait_start,notify,wait_end" {
		t.Fatalf("unexpected hook sequence: got %q, want wait_start,notify,wait_end", got)
	}
}

func TestAutonomousAndSleepChannelsHideContradictoryTools(t *testing.T) {
	for _, tc := range []struct {
		channel   string
		forbidden []string
	}{
		{
			channel:   "autonomous",
			forbidden: []string{"send_files", "defer_plan_continuation", "ask_clarification", "present_plan", "send_voice"},
		},
		{
			channel:   "sleep",
			forbidden: []string{"send_files", "defer_plan_continuation", "send_telegram", "generate_image", "schedule_task", "schedule_reminder", "schedule_run"},
		},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			reg := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
			reg.SetSchedulerService(&mockSchedulerService{})
			reg.SetTelegramConfig("tok", []string{"1"})
			reg.SetChannel(tc.channel)
			reg.SetUnattended(true)

			for _, name := range tc.forbidden {
				if hasTool(reg, name) {
					t.Fatalf("%s should not be offered on %s channel", name, tc.channel)
				}
				if _, err := reg.Execute(context.Background(), unattendedToolCall(name, map[string]any{})); err == nil {
					t.Fatalf("expected %s to be rejected on %s channel", name, tc.channel)
				}
			}
			_ = time.Second
		})
	}
}
