package router

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestResolveMessages_textOnly(t *testing.T) {
	r := New(nil, Config{MainModel: "granite4.1:8b"})
	msgs := []MediaMessage{
		{
			Message: ollama.Message{Role: "user", Content: "hello"},
		},
	}
	res, err := r.ResolveMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	if res.Messages[0].Content != "hello" {
		t.Fatalf("content = %q, want hello", res.Messages[0].Content)
	}
	if len(res.Attachments) != 0 {
		t.Fatalf("expected no attachments, got %d", len(res.Attachments))
	}
}

func TestResolveMessages_textAfterAudioHistory(t *testing.T) {
	r := New(nil, Config{
		MainModel:     "granite4.1:8b",
		AudioModel:    "gemma4:e4b",
		HasCapability: func(model, capability string) bool { return true },
	})
	msgs := []MediaMessage{
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "",
				Images:  []string{"ZmFrZQ=="}, // "fake" base64 — sanitize only, no Ollama call
			},
			ImageKinds:     []string{"audio"},
			Transcriptions: []string{"previous audio said hi"},
		},
		{
			Message: ollama.Message{Role: "assistant", Content: "Got it"},
		},
		{
			Message: ollama.Message{Role: "user", Content: "follow up text"},
		},
	}
	res, err := r.ResolveMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(res.Messages))
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Content != "follow up text" {
		t.Fatalf("last content = %q, want follow up text", last.Content)
	}
	if len(last.Images) != 0 {
		t.Fatalf("last message should have no images, got %d", len(last.Images))
	}
}

// wavPayload returns a base64 payload with a real WAV header, like a stored
// synthesized voice reply.
func wavPayload() string {
	header := []byte{
		'R', 'I', 'F', 'F', 0x24, 0, 0, 0,
		'W', 'A', 'V', 'E', 'f', 'm', 't', ' ',
	}
	return base64.StdEncoding.EncodeToString(header)
}

// TestResolveMessages_dropsMediaFromEveryHistoryRole covers the device bug
// where a synthesized voice reply stored on an assistant message was reloaded
// as an image and re-sent to the main model on every turn, which answered
// "audio input is not supported".
func TestResolveMessages_dropsMediaFromEveryHistoryRole(t *testing.T) {
	r := New(nil, Config{
		MainModel:   "granite4.1:8b",
		AudioModel:  "gemma4:e4b",
		VisionModel: "vision:latest",
		HasCapability: func(model, capability string) bool {
			return model != "granite4.1:8b"
		},
	})
	msgs := []MediaMessage{
		{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Ya llego el Papa a Uruguay.",
				Images:  []string{wavPayload()},
			},
			// Deliberately wrong metadata: the payload is audio but the kind
			// says image, as the old reload path produced.
			ImageKinds: []string{"image"},
		},
		{
			Message: ollama.Message{Role: "user", Content: "hola"},
		},
	}

	res, err := r.ResolveMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}
	for i, msg := range res.Messages {
		if len(msg.Images) != 0 {
			t.Fatalf("message %d (role=%s) carries %d media payload(s) to the main model", i, msg.Role, len(msg.Images))
		}
	}
	if res.Messages[0].Role != "assistant" || res.Messages[0].Content == "" {
		t.Fatalf("assistant history message was altered: %+v", res.Messages[0])
	}
}

// TestResolveMessages_keepsImagesWhenVisionIsOnMainModel ensures the guard
// above does not drop legitimate image passthrough.
func TestResolveMessages_keepsImagesWhenVisionIsOnMainModel(t *testing.T) {
	r := New(nil, Config{
		MainModel:     "granite4.1:8b",
		HasCapability: func(model, capability string) bool { return true },
	})
	pngPayload := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n0000"))
	msgs := []MediaMessage{
		{
			Message:    ollama.Message{Role: "user", Content: "", Images: []string{pngPayload}},
			ImageKinds: []string{"image"},
		},
	}
	res, err := r.ResolveMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}
	if len(res.Messages) != 1 || len(res.Messages[0].Images) != 1 {
		t.Fatalf("image passthrough was dropped: %+v", res.Messages)
	}
}
