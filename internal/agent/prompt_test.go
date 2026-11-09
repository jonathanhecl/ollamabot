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
}
