package docs

import (
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/capabilities"
)

func TestReferenceMentionsPendingAudioAndVideo(t *testing.T) {
	got := Reference(ReferenceData{BaseURL: "http://localhost:11434", OllamaVersion: "0.24.0", GeneratedAt: time.Unix(0, 0).UTC()})
	for _, want := range []string{"Audio", "Video", "messages[].images", "POST /api/chat"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reference missing %q", want)
		}
	}
}

func TestInventoryIncludesStatusSemantics(t *testing.T) {
	got := Inventory([]capabilities.ModelReport{{
		Name: "qwen3:8b",
		Capabilities: map[string]capabilities.Status{
			"completion": capabilities.Confirmed,
			"audio":      capabilities.Pending,
		},
	}}, ReferenceData{BaseURL: "http://localhost:11434", GeneratedAt: time.Unix(0, 0).UTC()})
	for _, want := range []string{"qwen3:8b", "comprobado", "inferido", "pendiente"} {
		if !strings.Contains(got, want) {
			t.Fatalf("inventory missing %q", want)
		}
	}
}
