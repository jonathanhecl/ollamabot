package router

import (
	"context"
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
		MainModel:   "granite4.1:8b",
		AudioModel:  "gemma4:e4b",
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
