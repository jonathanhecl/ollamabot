package sessions

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// TestFinalizeKeepsToolGeneratedAttachments guards the turn-scoped attachments:
// a tool saves the file and registers it on the assistant message the stream is
// recording, while finalHistory only carries text and tool calls. Without the
// merge the image/voice reference disappears from the saved session.
func TestFinalizeKeepsToolGeneratedAttachments(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(Session{ID: "sess-att", Title: "att", Model: "main"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	rec := NewRecorder(store, "sess-att", nil, "main", "web")

	rec.OnToolStart("send_voice", nil, "internal")
	rec.AddAttachmentRef("voice_1.wav", "audio/wav", filepath.Join(t.TempDir(), "voice_1.wav"))
	rec.OnContent("Listo.")
	rec.OnDone(ollama.ChatResponse{Done: true, DoneReason: "stop"})

	finalHistory := []ollama.Message{
		{Role: "user", Content: "decime algo"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{{Type: "function", Function: ollama.ToolFunction{Name: "send_voice"}}}},
		{Role: "tool", Content: "Voice note delivered.", Name: "send_voice"},
		{Role: "assistant", Content: "Listo."},
	}
	saved, err := rec.FinalizeAndSave(finalHistory)
	if err != nil {
		t.Fatalf("FinalizeAndSave: %v", err)
	}

	var found bool
	for _, raw := range saved {
		var msg RawMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, att := range msg.Attachments {
			if att.Name != "voice_1.wav" {
				continue
			}
			found = true
			if att.Kind != "audio" || att.Mime != "audio/wav" {
				t.Fatalf("unexpected attachment metadata: %#v", att)
			}
		}
	}
	if !found {
		t.Fatal("voice attachment was dropped when finalizing the turn")
	}
}
