package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

type RiskLevel int

const (
	RiskSafe RiskLevel = iota
	RiskNeedsApproval
	RiskBlocked
)

type RiskAssessment struct {
	Level   RiskLevel
	Summary string
	Label   string
}

type ApprovalPolicy int

const (
	ApprovalPolicyInteractive ApprovalPolicy = iota
	ApprovalPolicyAutonomous
)

// ClassifyToolRisk decides whether a tool can run without interrupting an
// autonomous task. Interactive mode may still be stricter than this assessment.
func ClassifyToolRisk(tool string, args map[string]any, workspace string) RiskAssessment {
	_, label := sessions.FormatApprovalSignature(tool, args, workspace)
	switch tool {
	case "write_file", "edit_file":
		filePath, _ := args["file_path"].(string)
		if filePath == "" {
			return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: "The target file path is missing, so the write cannot be safely scoped to the workspace."}
		}
		if _, err := ResolveAndValidatePath(workspace, filePath); err != nil {
			return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: "This file operation targets a path outside the configured workspace or an otherwise protected location."}
		}
		return RiskAssessment{Level: RiskSafe, Label: label, Summary: "This file operation is scoped to the configured workspace."}
	case "execute_command":
		return classifyCommandRisk(args, workspace, label)
	default:
		return RiskAssessment{Level: RiskSafe, Label: label, Summary: "This tool is read-only or does not perform a risky local action."}
	}
}

func classifyCommandRisk(args map[string]any, workspace string, label string) RiskAssessment {
	command, _ := args["command"].(string)
	base := filepath.Base(strings.TrimSpace(command))
	if base == "" {
		return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: "The command is empty, so it cannot be assessed safely."}
	}
	if !defaultAllowedCommands[base] {
		return RiskAssessment{Level: RiskBlocked, Label: label, Summary: fmt.Sprintf("The executable %q is not in the allowed command list.", base)}
	}

	argv := sessions.NormalizeExecuteCommandArgs(base, riskStringSlice(args["args"]), workspace)
	if containsShellOperator(base) {
		return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: "The executable name contains shell control characters."}
	}
	for _, arg := range argv {
		if containsShellOperator(arg) {
			return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: "The command contains shell operators, pipes, redirects, or command substitution that could hide additional actions."}
		}
	}
	if reason := dangerousSubcommand(base, argv); reason != "" {
		return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: reason}
	}
	if outside := firstPathOutsideWorkspace(argv, workspace); outside != "" {
		return RiskAssessment{Level: RiskNeedsApproval, Label: label, Summary: fmt.Sprintf("The command references a path outside the configured workspace: %s", outside)}
	}
	return RiskAssessment{Level: RiskSafe, Label: label, Summary: "This command uses an allowed executable and appears scoped to files inside the workspace."}
}

func containsShellOperator(s string) bool {
	return strings.ContainsAny(s, "|;&><`") ||
		strings.Contains(s, "$(") ||
		strings.Contains(s, "${") ||
		strings.Contains(s, "&&") ||
		strings.Contains(s, "||")
}

func dangerousSubcommand(base string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch base {
	case "git":
		sub := strings.TrimSpace(args[0])
		switch sub {
		case "push", "reset", "clean", "rebase", "checkout", "switch":
			return fmt.Sprintf("The git subcommand %q can rewrite, publish, or discard repository state.", sub)
		}
	case "npm", "yarn":
		sub := strings.TrimSpace(args[0])
		switch sub {
		case "publish", "adduser", "login", "logout", "owner", "token":
			return fmt.Sprintf("The package manager subcommand %q can publish packages or change account state.", sub)
		}
	}
	return ""
}

func firstPathOutsideWorkspace(args []string, workspace string) string {
	workspace = filepath.Clean(workspace)
	if workspace == "" {
		return ""
	}
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		candidate := arg
		if !filepath.IsAbs(candidate) {
			if !looksLikePath(candidate) {
				continue
			}
			candidate = filepath.Join(workspace, candidate)
		}
		candidate = filepath.Clean(candidate)
		rel, err := filepath.Rel(workspace, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return arg
		}
	}
	return ""
}

func looksLikePath(arg string) bool {
	if strings.Contains(arg, "/") || strings.Contains(arg, "\\") {
		return true
	}
	ext := filepath.Ext(arg)
	return ext != ""
}

func riskStringSlice(v any) []string {
	switch raw := v.(type) {
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string(nil), raw...)
	default:
		return nil
	}
}
