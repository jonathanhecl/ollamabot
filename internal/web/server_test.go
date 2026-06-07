package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
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

	out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
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

	out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
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

	out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
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

func TestResolveMedia_OnlyLatestUserMessageAnalyzed(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Second audio transcript.",
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
				Content: "First transcription output.",
				Images:  []string{"first_base64audio"},
			},
			ImageKinds: []string{"audio"},
		},
		{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Hello!",
			},
		},
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "",
				Images:  []string{"second_base64audio"},
			},
			ImageKinds: []string{"audio"},
		},
	}

	out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// For the first message (historical), it should remain unmodified and not split/transcribed again.
	// For the second message (assistant), it remains as is.
	// For the third message (latest user message), it should be processed and split into assistant analysis + user.
	// Total expected messages: 1 (first user) + 1 (assistant) + 2 (processed third user) = 4 messages.
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(out), out)
	}

	if calls != 1 {
		t.Errorf("expected exactly 1 pre-processing call to Ollama (for the latest user message), but got %d", calls)
	}

	if out[0].Content != "First transcription output." {
		t.Errorf("expected first user message to remain untouched, but got: %q", out[0].Content)
	}

	if out[2].Role != "assistant" || !strings.Contains(out[2].Content, "Second audio transcript.") {
		t.Errorf("expected third message (processed audio assistant block), got: %+v", out[2])
	}
}

func TestResolveMedia_ThreeConsecutiveAudios(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Third audio transcript.",
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
				Content: "First transcription output.",
				Images:  nil, // cleared on Turn 1 completion
			},
			ImageKinds: nil, // cleared on Turn 1 completion
		},
		{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "The user has attached media. The pre-processing analysis is as follows:\n\n[Audio Transcription & Analysis]:\nFirst transcription output.",
			},
		},
		{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Hello!",
			},
		},
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "Second transcription output.",
				Images:  nil, // cleared on Turn 2 completion
			},
			ImageKinds: nil, // cleared on Turn 2 completion
		},
		{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "The user has attached media. The pre-processing analysis is as follows:\n\n[Audio Transcription & Analysis]:\nSecond transcription output.",
			},
		},
		{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "How are you?",
			},
		},
		{
			Message: ollama.Message{
				Role:    "user",
				Content: "",
				Images:  []string{"third_base64audio"},
			},
			ImageKinds: []string{"audio"},
		},
	}

	out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 6 historical messages (user1, pre1, assistant1, user2, pre2, assistant2)
	// and 2 messages for Turn 3 (pre3, user3) = 8 messages total.
	if len(out) != 8 {
		t.Fatalf("expected 8 messages, got %d", len(out))
	}

	if calls != 1 {
		t.Errorf("expected exactly 1 pre-processing call to Ollama (for the third active audio), but got %d", calls)
	}

	if out[0].Content != "First transcription output." {
		t.Errorf("expected first user content untouched, got %q", out[0].Content)
	}
	if out[3].Content != "Second transcription output." {
		t.Errorf("expected second user content untouched, got %q", out[3].Content)
	}
	if out[6].Role != "assistant" || !strings.Contains(out[6].Content, "Third audio transcript.") {
		t.Errorf("expected pre3 to contain transcript, got: %+v", out[6])
	}
}

func TestClarifyTool(t *testing.T) {
	s := &Server{
		clarifications: make(map[string]chan string),
	}

	// Setup a pending clarification request channel
	clarifyID := "test_clarify_id"
	ch := make(chan string, 1)
	s.clarifications[clarifyID] = ch

	// Post response
	reqBody := `{"id": "test_clarify_id", "option": "Option B"}`
	req := httptest.NewRequest("POST", "/api/tools/clarify", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	s.handleClarifyTool(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	select {
	case chosen := <-ch:
		if chosen != "Option B" {
			t.Errorf("expected chosen option 'Option B', got %q", chosen)
		}
	default:
		t.Error("expected channel to receive the option")
	}
}

func TestSelectDefaultOption(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		want    string
	}{
		{
			name:    "selects recommended option",
			options: []string{"Option A", "Option B (Recommended)", "Option C"},
			want:    "Option B (Recommended)",
		},
		{
			name:    "selects recomendado option",
			options: []string{"Option A", "Option B (Recomendado)", "Option C"},
			want:    "Option B (Recomendado)",
		},
		{
			name:    "selects default option",
			options: []string{"Option A", "Default Option", "Option C"},
			want:    "Default Option",
		},
		{
			name:    "fallback to first option",
			options: []string{"Option A", "Option B", "Option C"},
			want:    "Option A",
		},
		{
			name:    "empty options slice",
			options: []string{},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectDefaultOption(tt.options)
			if got != tt.want {
				t.Errorf("selectDefaultOption() = %q, want %q", got, tt.want)
			}
		})
	}
}


