package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestInterceptImageCommand(t *testing.T) {
	out := InterceptImageCommand([]ollama.Message{{Role: "user", Content: "/image a red fox"}})
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if out[0].Content == "/image a red fox" {
		t.Fatal("expected /image command to be rewritten")
	}
	if !strings.Contains(out[0].Content, "generate_image") {
		t.Fatalf("expected generate_image instruction, got %q", out[0].Content)
	}

	// Non-image commands and non-user last messages are unchanged.
	if out := InterceptImageCommand([]ollama.Message{{Role: "user", Content: "hello"}}); out[0].Content != "hello" {
		t.Fatal("expected non-image command to be unchanged")
	}
	if out := InterceptImageCommand([]ollama.Message{{Role: "assistant", Content: "/image x"}}); out[0].Content != "/image x" {
		t.Fatal("expected assistant message to be unchanged")
	}
}

func TestLastAssistantContent(t *testing.T) {
	msgs := []ollama.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "first"},
		{Role: "assistant", Content: "   "},
		{Role: "assistant", Content: "last"},
	}
	if got := LastAssistantContent(msgs); got != "last" {
		t.Fatalf("expected 'last', got %q", got)
	}
	if got := LastAssistantContent([]ollama.Message{{Role: "assistant", Content: " "}}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := LastAssistantContent(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

func TestApplyMediaMetadata(t *testing.T) {
	msgs := []sessions.RawMsg{
		{Role: "user", Attachments: []sessions.AttachmentMeta{
			{Kind: "audio"},
			{Kind: "image"},
		}},
	}
	attachments := []router.AttachmentResult{
		{Index: 0, Kind: "audio", Model: "whisper", Transcription: "hello there", Unreadable: false},
		{Index: 1, Kind: "image", Model: "llava", Description: "a red fox"},
	}
	out := ApplyMediaMetadata(msgs, attachments)
	if len(out) != 1 || len(out[0].Attachments) != 2 {
		t.Fatalf("unexpected output shape: %#v", out)
	}
	if out[0].Attachments[0].Transcription != "hello there" || out[0].Attachments[0].ProcessedBy != "whisper" {
		t.Fatalf("audio metadata not applied: %#v", out[0].Attachments[0])
	}
	if out[0].Attachments[1].Description != "a red fox" || out[0].Attachments[1].ProcessedBy != "llava" {
		t.Fatalf("image metadata not applied: %#v", out[0].Attachments[1])
	}
	if out[0].Attachments[0].ProcessedAt == "" {
		t.Fatal("expected ProcessedAt to be set")
	}
}

func TestApplyMediaMetadataIgnoresOutOfRange(t *testing.T) {
	msgs := []sessions.RawMsg{
		{Role: "user", Attachments: []sessions.AttachmentMeta{{Kind: "image"}}},
	}
	attachments := []router.AttachmentResult{{Index: 5, Kind: "image", Description: "nope"}}
	out := ApplyMediaMetadata(msgs, attachments)
	if out[0].Attachments[0].Description != "" {
		t.Fatal("expected out-of-range index to be ignored")
	}
}

func TestMediaMessagesFromRaw(t *testing.T) {
	tcRaw, _ := json.Marshal(ollama.ToolCall{Type: "function", Function: ollama.ToolFunction{Name: "web_search"}})
	msgs := []sessions.RawMsg{
		{Role: "user", Content: "hi", Attachments: []sessions.AttachmentMeta{{Kind: "audio", Transcription: "transcribed"}}},
		{Role: "assistant", ToolCalls: []json.RawMessage{tcRaw}},
	}
	out := MediaMessagesFromRaw(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if len(out[0].Transcriptions) != 1 || out[0].Transcriptions[0] != "transcribed" {
		t.Fatalf("expected transcription, got %#v", out[0].Transcriptions)
	}
	if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].Function.Name != "web_search" {
		t.Fatalf("expected tool call, got %#v", out[1].ToolCalls)
	}
}

func TestAppendSystemNote(t *testing.T) {
	msgs := []ollama.Message{{Role: "system", Content: "base"}, {Role: "user", Content: "hi"}}
	out := appendSystemNote(msgs, "note")
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Content != "base\n\nnote" {
		t.Fatalf("expected note appended to system message, got %q", out[0].Content)
	}

	// No system message: prepend a new one.
	out = appendSystemNote([]ollama.Message{{Role: "user", Content: "hi"}}, "note")
	if len(out) != 2 || out[0].Role != "system" || out[0].Content != "note" {
		t.Fatalf("expected prepended system note, got %#v", out)
	}
}

func TestHasExt(t *testing.T) {
	if !hasExt("photo.PNG", ".png", ".jpg") {
		t.Fatal("expected case-insensitive extension match")
	}
	if hasExt("photo.txt", ".png", ".jpg") {
		t.Fatal("expected no match for .txt")
	}
	if hasExt("photo.pngx", ".png") {
		t.Fatal("expected no partial-extension match")
	}
}

func TestInjectContextEmptySessionID(t *testing.T) {
	msgs := []ollama.Message{{Role: "user", Content: "hi"}}
	out := InjectContext("", "", "", msgs)
	if len(out) != 1 || out[0].Content != "hi" {
		t.Fatalf("expected messages unchanged for empty session ID, got %#v", out)
	}
}
