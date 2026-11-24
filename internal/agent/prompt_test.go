package agent

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// TestStaticPrefixCarriesDeviceAndLanguageRules guards the prompt the model
// receives on the device channel: both blocks must reach the request, or the
// reply comes back long, in English and full of Markdown.
func TestStaticPrefixCarriesDeviceAndLanguageRules(t *testing.T) {
	cfg := config.Config{
		OllamaDefaultModel:  "main-model",
		DeviceReplyMaxChars: 400,
	}
	registry := tools.NewRegistry(false, t.TempDir(), nil, nil, "", tools.SearchConfig{})
	registry.SetChannel("device")
	a := NewAgent(config.NewManager(cfg), nil, registry)

	var joined strings.Builder
	for _, msg := range buildStaticSystemPrefix(a, "", "", "", "", "") {
		joined.WriteString(msg.Content)
		joined.WriteString("\n")
	}
	prefix := joined.String()

	if !strings.Contains(prefix, "## Device Mode") {
		t.Fatalf("device rules missing from the system prefix:\n%s", prefix)
	}
	if !strings.Contains(prefix, "400 characters") {
		t.Fatal("device reply limit missing from the prompt")
	}
	if !strings.Contains(prefix, "## Language and Regional Conventions") {
		t.Fatal("language rules missing from the system prefix")
	}
	if !strings.Contains(prefix, "Celsius") {
		t.Fatal("regional conventions missing from the system prefix")
	}
	for _, forbidden := range []string{"generate_image", "defer_plan_continuation"} {
		if strings.Contains(prefix, forbidden) {
			t.Fatalf("device prefix must not contain %q:\n%s", forbidden, prefix)
		}
	}
}

func TestUnattendedPrefixAndLoopHintsAvoidContradictions(t *testing.T) {
	cfg := config.Config{
		OllamaDefaultModel: "main-model",
		OllamaModelImage:   "image-model",
		OllamaModelEmbed:   "embed-model",
	}
	registry := tools.NewRegistry(false, t.TempDir(), nil, nil, "", tools.SearchConfig{})
	registry.SetChannel("autonomous")
	registry.SetUnattended(true)
	a := NewAgent(config.NewManager(cfg), nil, registry)

	var joined strings.Builder
	for _, msg := range buildStaticSystemPrefix(a, "Finish feature", "", "", "", "") {
		joined.WriteString(msg.Content)
		joined.WriteString("\n")
	}
	prefix := joined.String()

	for _, forbidden := range []string{
		"You have access to image generation via the `generate_image` tool",
		"You have access to long-term memory tools",
		"defer it with defer_plan_continuation",
	} {
		if strings.Contains(prefix, forbidden) {
			t.Fatalf("unattended autonomous prefix must not contain %q", forbidden)
		}
	}
	if !strings.Contains(prefix, "Complete it in one pass") {
		t.Fatalf("expected single-pass goal reinforcement in autonomous prefix, got:\n%s", prefix)
	}

	for _, toolName := range []string{"list_files", "execute_command", "read_file", "other_tool"} {
		hint := repetitiveLoopHint(toolName, registry)
		if strings.Contains(strings.ToLower(hint), "ask the user") {
			t.Fatalf("unattended loop hint for %s must not instruct asking the user: %q", toolName, hint)
		}
	}
}
