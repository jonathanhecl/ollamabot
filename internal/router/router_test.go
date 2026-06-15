package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestRouterModelGetters(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		wantVision  string
		wantAudio   string
		wantImage   string
		wantSteps   int
	}{
		{
			name: "All configured",
			cfg: Config{
				MainModel:   "main",
				VisionModel: "vision",
				AudioModel:  "audio",
				ImageModel:  "image",
				ImageSteps:  10,
			},
			wantVision: "vision",
			wantAudio:  "audio",
			wantImage:  "image",
			wantSteps:  10,
		},
		{
			name: "Fallback to main and defaults",
			cfg: Config{
				MainModel: "main",
			},
			wantVision: "main",
			wantAudio:  "main",
			wantImage:  "main",
			wantSteps:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(nil, tt.cfg)
			if got := r.visionModel(); got != tt.wantVision {
				t.Errorf("visionModel() = %q, want %q", got, tt.wantVision)
			}
			if got := r.audioModel(); got != tt.wantAudio {
				t.Errorf("audioModel() = %q, want %q", got, tt.wantAudio)
			}
			if got := r.imageModel(); got != tt.wantImage {
				t.Errorf("imageModel() = %q, want %q", got, tt.wantImage)
			}
			if got := r.imageSteps(); got != tt.wantSteps {
				t.Errorf("imageSteps() = %d, want %d", got, tt.wantSteps)
			}
		})
	}
}

func TestRouterDecide(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		kind     string
		expected Decision
	}{
		{
			name: "Unsupported kind",
			cfg: Config{
				MainModel: "main",
			},
			kind:     "text",
			expected: DecisionUnsupported,
		},
		{
			name: "Dedicated image model different from main",
			cfg: Config{
				MainModel:   "main",
				VisionModel: "vision",
			},
			kind:     "image",
			expected: DecisionRoute,
		},
		{
			name: "Dedicated audio model different from main",
			cfg: Config{
				MainModel:  "main",
				AudioModel: "audio",
			},
			kind:     "audio",
			expected: DecisionRoute,
		},
		{
			name: "No dedicated model, capabilities unknown (nil)",
			cfg: Config{
				MainModel:     "main",
				HasCapability: nil,
			},
			kind:     "image",
			expected: DecisionPassthrough,
		},
		{
			name: "No dedicated model, main has capability",
			cfg: Config{
				MainModel: "main",
				HasCapability: func(model, cap string) bool {
					return model == "main" && cap == "vision"
				},
			},
			kind:     "image",
			expected: DecisionPassthrough,
		},
		{
			name: "No dedicated model, main lacks capability",
			cfg: Config{
				MainModel: "main",
				HasCapability: func(model, cap string) bool {
					return false
				},
			},
			kind:     "image",
			expected: DecisionUnsupported,
		},
		{
			name: "Dedicated model same as main",
			cfg: Config{
				MainModel:   "main",
				VisionModel: "main",
				HasCapability: func(model, cap string) bool {
					return false // Even if main lacks capability, user forced it
				},
			},
			kind:     "image",
			expected: DecisionPassthrough,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(nil, tt.cfg)
			got := r.Decide(tt.kind)
			if got != tt.expected {
				t.Errorf("Decide(%q) = %q, want %q", tt.kind, got, tt.expected)
			}
		})
	}
}

func TestGenerateImage(t *testing.T) {
	// Case 1: No image model configured
	rNoModel := New(nil, Config{})
	_, err := rNoModel.GenerateImage(context.Background(), "a cute cat", 512, 512, 42, nil)
	if err == nil {
		t.Fatal("expected error when image model is empty, got nil")
	}

	// Case 2: Server returns chunks and completes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("expected POST /api/generate, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		// Send stream chunk 1
		c1 := ollama.GenerateResponse{Completed: 1, Total: 4, Done: false}
		data1, _ := json.Marshal(c1)
		_, _ = w.Write(append(data1, '\n'))

		// Send stream chunk 2 (final)
		c2 := ollama.GenerateResponse{Completed: 4, Total: 4, Done: true, Image: "mock-base64-image"}
		data2, _ := json.Marshal(c2)
		_, _ = w.Write(append(data2, '\n'))
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	r := New(client, Config{ImageModel: "imagen-model", ImageSteps: 4})

	var progressCalls int
	onProgress := func(completed, total int, status string) {
		progressCalls++
		if total != 4 {
			t.Errorf("expected total=4, got %d", total)
		}
		if status != "generating" {
			t.Errorf("expected status=generating, got %q", status)
		}
	}

	img, err := r.GenerateImage(context.Background(), "a cute cat", 512, 512, 42, onProgress)
	if err != nil {
		t.Fatalf("GenerateImage failed: %v", err)
	}

	if img != "mock-base64-image" {
		t.Errorf("expected mock-base64-image, got %q", img)
	}

	if progressCalls != 2 {
		t.Errorf("expected 2 progress callback calls, got %d", progressCalls)
	}

	// Case 3: Server fails
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer failServer.Close()

	failClient := ollama.NewClient(failServer.URL)
	rFail := New(failClient, Config{ImageModel: "imagen-model"})
	_, err = rFail.GenerateImage(context.Background(), "a cute cat", 512, 512, 42, nil)
	if err == nil {
		t.Fatal("expected error on server failure, got nil")
	}
}

func TestAnalyzeImage(t *testing.T) {
	var receivedRequest ollama.ChatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected POST /api/chat, got %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedRequest)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "This is a beautiful cat",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	r := New(client, Config{VisionModel: "vision-model"})

	// Case 1: Custom prompt
	desc, err := r.AnalyzeImage(context.Background(), "base64-data", "What color is the cat?")
	if err != nil {
		t.Fatalf("AnalyzeImage failed: %v", err)
	}
	if desc != "This is a beautiful cat" {
		t.Errorf("expected 'This is a beautiful cat', got %q", desc)
	}
	if len(receivedRequest.Messages) != 1 || receivedRequest.Messages[0].Content != "What color is the cat?" {
		t.Errorf("expected prompt content to be 'What color is the cat?', got %q", receivedRequest.Messages[0].Content)
	}
	if len(receivedRequest.Messages[0].Images) != 1 || receivedRequest.Messages[0].Images[0] != "base64-data" {
		t.Errorf("expected image to be base64-data, got %v", receivedRequest.Messages[0].Images)
	}

	// Case 2: Default prompt
	_, err = r.AnalyzeImage(context.Background(), "base64-data-2", "")
	if err != nil {
		t.Fatalf("AnalyzeImage with default prompt failed: %v", err)
	}
	if receivedRequest.Messages[0].Content != imageAnalysisPrompt {
		t.Errorf("expected default image analysis prompt, got %q", receivedRequest.Messages[0].Content)
	}

	// Case 3: Error
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer failServer.Close()
	failClient := ollama.NewClient(failServer.URL)
	rFail := New(failClient, Config{VisionModel: "vision-model"})
	_, err = rFail.AnalyzeImage(context.Background(), "base64", "")
	if err == nil {
		t.Fatal("expected error on failed chat request, got nil")
	}
}

func TestTranscribeAudio(t *testing.T) {
	// Case 1: Empty base64data
	rEmpty := New(nil, Config{AudioModel: "audio-model"})
	_, err := rEmpty.TranscribeAudio(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty base64, got nil")
	}

	// Case 2: Valid transcription response (Attempt 1 success)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		transcription := AudioTranscription{
			Transcription: "Hello world",
			Language:      "en",
			Sounds:        "cough",
			Unreadable:    false,
		}
		raw, _ := json.Marshal(transcription)

		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: string(raw),
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	r := New(client, Config{AudioModel: "audio-model"})

	out, err := r.TranscribeAudio(context.Background(), "base64-audio")
	if err != nil {
		t.Fatalf("TranscribeAudio failed: %v", err)
	}
	if out.Transcription != "Hello world" || out.Language != "en" || out.Sounds != "cough" || out.Unreadable {
		t.Errorf("unexpected output struct: %+v", out)
	}

	// Case 3: First attempt returns invalid JSON, second attempt returns valid JSON
	var attemptCount int
	attemptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var content string
		if attemptCount == 1 {
			content = "bad-json"
		} else {
			transcription := AudioTranscription{
				Transcription: "Recovered speech",
				Language:      "none", // test normalization
				Sounds:        "nothing", // test normalization
			}
			raw, _ := json.Marshal(transcription)
			content = string(raw)
		}

		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: content,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer attemptServer.Close()

	clientAttempt := ollama.NewClient(attemptServer.URL)
	rAttempt := New(clientAttempt, Config{AudioModel: "audio-model"})

	outAttempt, err := rAttempt.TranscribeAudio(context.Background(), "base64-audio")
	if err != nil {
		t.Fatalf("TranscribeAudio with retry failed: %v", err)
	}
	if outAttempt.Transcription != "Recovered speech" {
		t.Errorf("expected 'Recovered speech', got %q", outAttempt.Transcription)
	}
	// normalizeNone should clear "none" and "nothing"
	if outAttempt.Language != "" || outAttempt.Sounds != "" {
		t.Errorf("expected Language and Sounds to be normalized to empty, got lang=%q, sounds=%q", outAttempt.Language, outAttempt.Sounds)
	}

	// Case 4: Both attempts return invalid JSON, fallback to raw text as transcription
	attemptCount = 0
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: "Just raw speech text",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer fallbackServer.Close()

	clientFallback := ollama.NewClient(fallbackServer.URL)
	rFallback := New(clientFallback, Config{AudioModel: "audio-model"})

	outFallback, err := rFallback.TranscribeAudio(context.Background(), "base64-audio")
	if err != nil {
		t.Fatalf("TranscribeAudio fallback failed: %v", err)
	}
	if outFallback.Transcription != "Just raw speech text" {
		t.Errorf("expected fallback to raw transcription text, got %q", outFallback.Transcription)
	}

	// Case 5: Empty transcription and sounds marks it as unreadable
	unreadableServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		transcription := AudioTranscription{
			Transcription: "",
			Sounds:        "",
		}
		raw, _ := json.Marshal(transcription)

		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: string(raw),
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer unreadableServer.Close()

	clientUnreadable := ollama.NewClient(unreadableServer.URL)
	rUnreadable := New(clientUnreadable, Config{AudioModel: "audio-model"})

	outUnreadable, err := rUnreadable.TranscribeAudio(context.Background(), "base64-audio")
	if err != nil {
		t.Fatalf("TranscribeAudio failed: %v", err)
	}
	if !outUnreadable.Unreadable {
		t.Error("expected Unreadable to be true when transcription and sounds are empty")
	}

	// Case 6: Connection fails completely
	rFail := New(ollama.NewClient("http://localhost:12345/"), Config{AudioModel: "audio-model"})
	_, err = rFail.TranscribeAudio(context.Background(), "base64-audio")
	if err == nil {
		t.Fatal("expected error on connection failure, got nil")
	}
}

func TestResolveMessagesAdditionalCases(t *testing.T) {
	// Let's add extra coverage for Decide and sanitizeHistoryMessage/resolveActiveUserMessage.
	// We want to test DecisionUnsupported for Decide("image") and Decide("audio").
	r := New(nil, Config{
		MainModel: "main",
		HasCapability: func(model, capability string) bool {
			return false // main does not support anything
		},
	})

	// Decide checks
	if dec := r.Decide("image"); dec != DecisionUnsupported {
		t.Errorf("Decide(image) = %v, want DecisionUnsupported", dec)
	}
	if dec := r.Decide("audio"); dec != DecisionUnsupported {
		t.Errorf("Decide(audio) = %v, want DecisionUnsupported", dec)
	}

	// History sanitization with DecisionUnsupported
	msg := MediaMessage{
		Message: ollama.Message{
			Role:    "user",
			Content: "hello",
			Images:  []string{"image-data", "audio-data"},
		},
		ImageKinds: []string{"image", "audio"},
	}

	sanitized := r.sanitizeHistoryMessage(msg)
	// Since Decide("image") is DecisionUnsupported, dropImages is r.Decide("image") != DecisionPassthrough, which is true.
	// Therefore, images are dropped.
	// Since kind == "audio" is skipped in kept list, kept will have 0 elements.
	if len(sanitized.Images) != 0 {
		t.Errorf("expected 0 images, got %d", len(sanitized.Images))
	}

	// Active user message resolution with DecisionUnsupported
	res, err := r.ResolveMessages(context.Background(), []MediaMessage{msg})
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}

	// Should have 1 attachment for image and 1 for audio, both skipped
	if len(res.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(res.Attachments))
	}
	if res.Attachments[0].Action != ActionSkipped || res.Attachments[1].Action != ActionSkipped {
		t.Errorf("expected both skipped, got actions: %q, %q", res.Attachments[0].Action, res.Attachments[1].Action)
	}

	// Test case where Decide is DecisionPassthrough (no mock calls needed for DecisionPassthrough, because no audio routing or vision routing is done)
	serverPT := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
			Message: ollama.Message{Role: "assistant", Content: `{"transcription": "hello"}`},
		})
	}))
	defer serverPT.Close()

	clientPT := ollama.NewClient(serverPT.URL)
	rPassthrough := New(clientPT, Config{
		MainModel: "main",
		HasCapability: func(model, capability string) bool {
			return true // supports everything natively
		},
	})
	resPT, err := rPassthrough.ResolveMessages(context.Background(), []MediaMessage{msg})
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}
	if len(resPT.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(resPT.Attachments))
	}
}

func TestResolveMessagesHelperErrors(t *testing.T) {
	// TranscribeAudio fails, AnalyzeImage fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	r := New(client, Config{
		MainModel:   "main",
		VisionModel: "vision",
		AudioModel:  "audio",
	})

	msg := MediaMessage{
		Message: ollama.Message{
			Role:    "user",
			Content: "hello",
			Images:  []string{"image-data", "audio-data"},
		},
		ImageKinds: []string{"image", "audio"},
	}

	res, err := r.ResolveMessages(context.Background(), []MediaMessage{msg})
	if err != nil {
		t.Fatalf("ResolveMessages: %v", err)
	}

	// Both should be annotated with error notes or skipped/unreadable.
	if len(res.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(res.Attachments))
	}

	audioResult := res.Attachments[0]
	if !audioResult.Unreadable {
		t.Errorf("expected audio to be unreadable on failure")
	}

	imageResult := res.Attachments[1]
	if imageResult.Action != ActionSkipped {
		t.Errorf("expected image to be skipped on analysis failure, got %s", imageResult.Action)
	}
}

type badReader struct{}

func (badReader) Read(p []byte) (int, error) {
	return 0, errors.New("bad read")
}
