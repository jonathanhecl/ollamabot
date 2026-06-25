package tools

import "testing"

func TestClassifyToolRiskSafeWorkspaceCommand(t *testing.T) {
	workspace := t.TempDir()
	got := ClassifyToolRisk("execute_command", map[string]any{
		"command": "python3",
		"args":    []any{"test_extraction_script.py"},
	}, workspace)
	if got.Level != RiskSafe {
		t.Fatalf("expected safe command, got level=%v summary=%q", got.Level, got.Summary)
	}
}

func TestClassifyToolRiskDangerousCommands(t *testing.T) {
	workspace := t.TempDir()
	cases := []map[string]any{
		{"command": "git", "args": []any{"push"}},
		{"command": "git", "args": []any{"reset", "--hard"}},
		{"command": "python3", "args": []any{"script.py", "|", "sh"}},
	}
	for _, args := range cases {
		got := ClassifyToolRisk("execute_command", args, workspace)
		if got.Level != RiskNeedsApproval {
			t.Fatalf("expected approval for %#v, got level=%v summary=%q", args, got.Level, got.Summary)
		}
	}
}

func TestClassifyToolRiskPathOutsideWorkspace(t *testing.T) {
	got := ClassifyToolRisk("execute_command", map[string]any{
		"command": "python3",
		"args":    []any{"../outside.py"},
	}, t.TempDir())
	if got.Level != RiskNeedsApproval {
		t.Fatalf("expected outside path to need approval, got level=%v summary=%q", got.Level, got.Summary)
	}
}
