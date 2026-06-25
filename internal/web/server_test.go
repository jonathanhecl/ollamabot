package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
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

	_, out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}

	if out[0].Role != "assistant" {
		t.Errorf("expected first message role assistant, got %s", out[0].Role)
	}
	want := "Media pre-processing context (produced by a vision model, not written by the user):\n\n[Image 1 analysis by vision]:\nIt shows a red balloon against a blue sky."
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
				Content: `{"transcription": "hello in an emphatic tone", "language": "en", "sounds": "", "unreadable": false}`,
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

	_, out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Routed audio produces a single resolved user message (no synthetic
	// assistant message): the transcription is injected inline.
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}

	if out[0].Role != "user" {
		t.Errorf("expected message role user, got %s", out[0].Role)
	}
	if out[0].Content != "[Transcription of the audio message]: \"hello in an emphatic tone\"" {
		t.Errorf("expected transcribed audio user content, got %q", out[0].Content)
	}
	if len(out[0].Images) != 0 {
		t.Errorf("expected 0 images (routed audio), got %d", len(out[0].Images))
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

	_, out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
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
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		content := "Detailed image description."
		if req.Format != nil {
			// Structured transcription request (audio).
			content = `{"transcription": "compare them please", "language": "en", "sounds": "", "unreadable": false}`
		}
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: content,
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

	_, out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
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
	// Passthrough audio still gets its transcription injected as text context.
	if !strings.Contains(out[1].Content, "compare them please") {
		t.Errorf("expected transcription injected in user content, got %q", out[1].Content)
	}
}

func TestResolveMedia_OnlyLatestUserMessageAnalyzed(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: `{"transcription": "Second audio transcript.", "language": "en", "sounds": "", "unreadable": false}`,
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

	_, out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The first (historical) user message is sanitized (audio base64 dropped,
	// text kept) and NOT re-transcribed. The assistant message stays as is.
	// The latest user message is resolved in place with the transcription
	// injected inline. Total: 3 messages.
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(out), out)
	}

	if calls != 1 {
		t.Errorf("expected exactly 1 pre-processing call to Ollama (for the latest user message), but got %d", calls)
	}

	if out[0].Content != "First transcription output." {
		t.Errorf("expected first user message text to remain, but got: %q", out[0].Content)
	}
	if len(out[0].Images) != 0 {
		t.Errorf("expected historical audio base64 to be dropped, got %d images", len(out[0].Images))
	}

	if out[2].Role != "user" || !strings.Contains(out[2].Content, "Second audio transcript.") {
		t.Errorf("expected resolved user message with transcript, got: %+v", out[2])
	}
	if len(out[2].Images) != 0 {
		t.Errorf("expected routed audio not to passthrough, got %d images", len(out[2].Images))
	}
}

func TestResolveMedia_ThreeConsecutiveAudios(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := ollama.ChatResponse{
			Message: ollama.Message{
				Role:    "assistant",
				Content: `{"transcription": "Third audio transcript.", "language": "en", "sounds": "", "unreadable": false}`,
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

	_, out, err := resolveMedia(context.Background(), mr, messages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 6 historical messages (user1, pre1, assistant1, user2, pre2, assistant2)
	// and 1 resolved user message for Turn 3 = 7 messages total.
	if len(out) != 7 {
		t.Fatalf("expected 7 messages, got %d", len(out))
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
	if out[6].Role != "user" || !strings.Contains(out[6].Content, "Third audio transcript.") {
		t.Errorf("expected resolved user message to contain transcript, got: %+v", out[6])
	}
}

func TestClarifyTool(t *testing.T) {
	s := &Server{
		cfgMgr:         config.NewManager(config.Config{}),
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

func TestAuthentication(t *testing.T) {
	s := &Server{
		cfgMgr: config.NewManager(config.Config{
			ServerPassword: "secretpassword",
		}),
	}

	handler := s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Case 1: Unprotected path (/api/health) should not require authentication
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected unprotected path /api/health to succeed, got %d", w.Result().StatusCode)
	}

	// Case 2: Protected path (/api/settings) without password should return 401 Unauthorized
	req = httptest.NewRequest("GET", "/api/settings", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("expected protected path without auth to return 401, got %d", w.Result().StatusCode)
	}

	// Case 3: Protected path (/api/settings) with correct X-Server-Password header should succeed
	req = httptest.NewRequest("GET", "/api/settings", nil)
	req.Header.Set("X-Server-Password", "secretpassword")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected protected path with X-Server-Password header to succeed, got %d", w.Result().StatusCode)
	}

	// Case 4: Protected path (/api/settings) with correct Bearer token in Authorization header should succeed
	req = httptest.NewRequest("GET", "/api/settings", nil)
	req.Header.Set("Authorization", "Bearer secretpassword")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected protected path with Authorization header to succeed, got %d", w.Result().StatusCode)
	}

	// Case 5: Protected path (/api/settings) with incorrect password should return 401 Unauthorized
	req = httptest.NewRequest("GET", "/api/settings", nil)
	req.Header.Set("X-Server-Password", "wrongpassword")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("expected protected path with incorrect password to return 401, got %d", w.Result().StatusCode)
	}

	// Case 6: Non-API path (e.g. static files like /index.html) should not require authentication
	req = httptest.NewRequest("GET", "/index.html", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected static path /index.html to succeed without auth, got %d", w.Result().StatusCode)
	}
}

func writeTestSkill(t *testing.T, root, folder, content string) {
	t.Helper()
	dir := filepath.Join(root, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsAPI(t *testing.T) {
	skillsRoot := t.TempDir()
	writeTestSkill(t, skillsRoot, "weather", `---
name: weather
description: Weather helper
homepage: https://example.com/weather
---

## Description
Weather helper

## Instructions
- [ ] Check current conditions
- [ ] Summarize forecast
`)

	s := &Server{
		cfgMgr: config.NewManager(config.Config{
			SkillsPath:    skillsRoot,
			SkillsPathRaw: skillsRoot,
		}),
	}

	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/skills", nil)
		w := httptest.NewRecorder()
		s.handleListSkills(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("list status = %d", w.Result().StatusCode)
		}
		var payload map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if int(payload["count"].(float64)) != 1 {
			t.Fatalf("expected count 1, got %#v", payload["count"])
		}
	})

	t.Run("get", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/skills/weather", nil)
		req.SetPathValue("name", "weather")
		w := httptest.NewRecorder()
		s.handleGetSkill(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("get status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		var detail map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail["name"] != "weather" {
			t.Fatalf("unexpected name: %#v", detail["name"])
		}
		steps, ok := detail["steps"].([]any)
		if !ok || len(steps) != 2 {
			t.Fatalf("expected 2 steps, got %#v", detail["steps"])
		}
	})

	t.Run("update", func(t *testing.T) {
		body := `{"description":"Updated helper","homepage":"https://example.com/new","instructions":"- [ ] Updated step"}`
		req := httptest.NewRequest("PUT", "/api/skills/weather", strings.NewReader(body))
		req.SetPathValue("name", "weather")
		w := httptest.NewRecorder()
		s.handleUpdateSkill(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("update status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		var detail map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail["description"] != "Updated helper" {
			t.Fatalf("unexpected description: %#v", detail["description"])
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/skills/weather", nil)
		req.SetPathValue("name", "weather")
		w := httptest.NewRecorder()
		s.handleDeleteSkill(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("delete status = %d", w.Result().StatusCode)
		}
		if _, err := os.Stat(filepath.Join(skillsRoot, "weather")); !os.IsNotExist(err) {
			t.Fatalf("expected skill directory removed, err=%v", err)
		}
	})

	t.Run("get missing", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/skills/missing", nil)
		req.SetPathValue("name", "missing")
		w := httptest.NewRecorder()
		s.handleGetSkill(w, req)
		if w.Result().StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Result().StatusCode)
		}
	})

	t.Run("invalid name", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/skills/../../etc", nil)
		req.SetPathValue("name", "../../etc")
		w := httptest.NewRecorder()
		s.handleGetSkill(w, req)
		if w.Result().StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 for invalid name, got %d", w.Result().StatusCode)
		}
	})
}
