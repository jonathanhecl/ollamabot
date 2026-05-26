package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
)

func TestResolveMedia_SplitsIntoAssistantAndUser(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "It shows a red balloon against a blue sky.",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := ollama.NewClient(ts.URL)
	mr := router.New(client, router.Config{
		MainModel:   "main",
		VisionModel: "vision",
	})

	messages := []MediaMessage{
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "What do you see?",
				Images:  []string{"base64imgdata"},
			},
			ImageKinds: []string{"image"},
		},
	}

	out, err := resolveMedia(context.Background(), mr, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}

	if out[0].Role != "assistant" {
		t.Errorf("expected first message role assistant, got %s", out[0].Role)
	}
	want := "The user has attached media. The pre-processing analysis is as follows:\n\n[Image Analysis (Prompt: What do you see?)]:\nIt shows a red balloon against a blue sky."
	if out[0].Content != want {
		t.Errorf("unexpected assistant content:\ngot  %q\nwant %q", out[0].Content, want)
	}

	if out[1].Role != "user" {
		t.Errorf("expected second message role user, got %s", out[1].Role)
	}
	if out[1].Content != "What do you see?" {
		t.Errorf("unexpected user content: %q", out[1].Content)
	}
	if len(out[1].Images) != 0 {
		t.Errorf("expected 0 images in user message, got %d", len(out[1].Images))
	}

	if calls != 1 {
		t.Errorf("expected 1 analysis call, got %d", calls)
	}
}

func TestResolveMedia_EmptyUserText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "The audio says hello in an emphatic tone.",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := ollama.NewClient(ts.URL)
	mr := router.New(client, router.Config{
		MainModel:  "main",
		AudioModel: "audio",
	})

	messages := []MediaMessage{
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "",
				Images:  []string{"base64audiodata"},
			},
			ImageKinds: []string{"audio"},
		},
	}

	out, err := resolveMedia(context.Background(), mr, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}

	if out[0].Role != "assistant" {
		t.Errorf("expected first message role assistant, got %s", out[0].Role)
	}
	want := "The user has attached media. The pre-processing analysis is as follows:\n\n[Audio Transcription & Analysis]:\nThe audio says hello in an emphatic tone."
	if out[0].Content != want {
		t.Errorf("unexpected assistant content: got %q, want %q", out[0].Content, want)
	}

	if out[1].Role != "user" {
		t.Errorf("expected second message role user, got %s", out[1].Role)
	}
	if out[1].Content != "[The user sent only this audio transcription]:\n\"The audio says hello in an emphatic tone.\"" {
		t.Errorf("expected transcribed audio user content, got %q", out[1].Content)
	}
}

func TestResolveMedia_NoRoutingNeeded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not call Ollama when no dedicated model is configured")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := ollama.NewClient(ts.URL)
	mr := router.New(client, router.Config{
		MainModel: "main",
	})

	messages := []MediaMessage{
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "Hello",
				Images:  []string{"base64imgdata"},
			},
			ImageKinds: []string{"image"},
		},
	}

	out, err := resolveMedia(context.Background(), mr, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if out[0].Role != "user" || out[0].Content != "Hello" || len(out[0].Images) != 1 {
		t.Errorf("unexpected message: %+v", out[0])
	}
}

func TestResolveMedia_PassthroughMixedWithAnalysis(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Detailed image description.",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := ollama.NewClient(ts.URL)
	mr := router.New(client, router.Config{
		MainModel:   "main",
		VisionModel: "vision",
		// AudioModel intentionally empty so audio passes through
	})

	messages := []MediaMessage{
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "Compare these",
				Images:  []string{"base64img", "base64audio"},
			},
			ImageKinds: []string{"image", "audio"},
		},
	}

	out, err := resolveMedia(context.Background(), mr, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}

	if out[0].Role != "assistant" {
		t.Errorf("expected assistant, got %s", out[0].Role)
	}
	if out[1].Role != "user" {
		t.Errorf("expected user, got %s", out[1].Role)
	}
	if len(out[1].Images) != 1 {
		t.Errorf("expected 1 passthrough image, got %d", len(out[1].Images))
	}
	if out[1].Images[0] != "base64audio" {
		t.Errorf("expected passthrough base64audio, got %s", out[1].Images[0])
	}
}
