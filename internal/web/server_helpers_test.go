package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestUploadsDir(t *testing.T) {
	got := uploadsDir("workspace", "sessions", "sess123")
	// We can normalize slash characters to support any platform
	gotClean := strings.ReplaceAll(got, "\\", "/")
	expectedClean := "sessions/sess123/uploads"
	if gotClean != expectedClean {
		t.Errorf("uploadsDir() = %q; want %q", gotClean, expectedClean)
	}
}

func TestSanitizeUploadName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"safe.txt", "safe.txt"},
		{"path/to/file.jpg", "file.jpg"},
		{"..\\..\\backdoor.sh", "backdoor.sh"},
		{"", "file"},
		{".", "file"},
		// String longer than 200 characters
		{strings.Repeat("a", 210) + ".png", strings.Repeat("a", 196) + ".png"},
	}

	for _, tt := range tests {
		got := sanitizeUploadName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeUploadName(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{2048, "2.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{3 * 1024 * 1024, "3.0 MB"},
	}

	for _, tt := range tests {
		got := humanSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("humanSize(%d) = %q; want %q", tt.bytes, got, tt.expected)
		}
	}
}

func TestDetectMime(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"test.pdf", "application/pdf"},
		{"test.txt", "text/plain"},
		{"test.md", "text/plain"},
		{"test.csv", "text/plain"},
		{"test.log", "text/plain"},
		{"test.json", "application/json"},
		{"test.html", "text/html"},
		{"test.htm", "text/html"},
		{"test.xml", "application/xml"},
		{"test.zip", "application/zip"},
		{"test.mp4", "video/mp4"},
		{"test.mkv", "video/x-matroska"},
		{"test.avi", "video/x-msvideo"},
		{"test.mov", "video/quicktime"},
		{"test.mp3", "audio/mpeg"},
		{"test.wav", "audio/wav"},
		{"test.py", "text/x-python"},
		{"test.go", "text/x-go"},
		{"test.js", "text/javascript"},
		{"test.ts", "text/typescript"},
		{"test.rs", "text/x-rust"},
		{"test.c", "text/x-c"},
		{"test.h", "text/x-c"},
		{"test.cpp", "text/x-c++"},
		{"test.hpp", "text/x-c++"},
		{"test.java", "text/x-java"},
		{"test.sh", "text/x-shellscript"},
		{"test.bash", "text/x-shellscript"},
		{"test.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		got := detectMime(tt.filename)
		if got != tt.expected {
			t.Errorf("detectMime(%q) = %q; want %q", tt.filename, got, tt.expected)
		}
	}
}

func TestWriteJSONAndWriteError(t *testing.T) {
	// Test writeJSON
	w1 := httptest.NewRecorder()
	val := map[string]int{"a": 1}
	writeJSON(w1, http.StatusCreated, val)

	resp1 := w1.Result()
	if resp1.StatusCode != http.StatusCreated {
		t.Errorf("writeJSON status = %d; want %d", resp1.StatusCode, http.StatusCreated)
	}
	if ct := resp1.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("writeJSON content-type = %q; want %q", ct, "application/json")
	}
	var gotVal map[string]int
	if err := json.NewDecoder(resp1.Body).Decode(&gotVal); err != nil {
		t.Fatalf("failed to decode writeJSON body: %v", err)
	}
	if gotVal["a"] != 1 {
		t.Errorf("writeJSON value expected a:1, got %v", gotVal)
	}

	// Test writeError
	w2 := httptest.NewRecorder()
	writeError(w2, http.StatusBadRequest, errors.New("test error"))
	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("writeError status = %d; want %d", resp2.StatusCode, http.StatusBadRequest)
	}
	var gotErr map[string]string
	if err := json.NewDecoder(resp2.Body).Decode(&gotErr); err != nil {
		t.Fatalf("failed to decode writeError body: %v", err)
	}
	if gotErr["error"] != "test error" {
		t.Errorf("writeError message = %q; want %q", gotErr["error"], "test error")
	}
}

func TestHandleHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.1.50"}`))
	}))
	defer ts.Close()

	client := ollama.NewClient(ts.URL)
	s := &Server{
		client: client,
	}

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("handleHealth status = %d; want %d", resp.StatusCode, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("handleHealth status field = %q; want %q", body["status"], "ok")
	}
	if body["ollama_version"] != "0.1.50" {
		t.Errorf("handleHealth ollama_version = %q; want %q", body["ollama_version"], "0.1.50")
	}
}

func TestHandleHealth_Error(t *testing.T) {
	client := ollama.NewClient("http://127.0.0.1:59999") // unlikely to be running
	s := &Server{
		client: client,
	}

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("handleHealth status = %d; want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestRouterConfig(t *testing.T) {
	cfg := config.Config{
		OllamaDefaultModel: "main-model",
		OllamaModelVision:  "vision-model",
		OllamaModelAudio:   "audio-model",
		OllamaModelImage:   "image-model",
		OllamaImageSteps:   10,
	}

	rc := routerConfig(cfg)
	if rc.MainModel != "main-model" {
		t.Errorf("routerConfig MainModel = %q; want %q", rc.MainModel, "main-model")
	}
	if rc.VisionModel != "vision-model" {
		t.Errorf("routerConfig VisionModel = %q; want %q", rc.VisionModel, "vision-model")
	}
	if rc.AudioModel != "audio-model" {
		t.Errorf("routerConfig AudioModel = %q; want %q", rc.AudioModel, "audio-model")
	}
	if rc.ImageModel != "image-model" {
		t.Errorf("routerConfig ImageModel = %q; want %q", rc.ImageModel, "image-model")
	}
	if rc.ImageSteps != 10 {
		t.Errorf("routerConfig ImageSteps = %d; want %d", rc.ImageSteps, 10)
	}
}

func TestRunningByName(t *testing.T) {
	models := []ollama.RunningModel{
		{Name: "llama3:latest", Model: "llama3:latest"},
		{Name: "gemma:7b", Model: "gemma:7b"},
	}

	m := runningByName(models)
	if _, ok := m["llama3:latest"]; !ok {
		t.Error("runningByName: expected 'llama3:latest' to be mapped")
	}
	if _, ok := m["gemma:7b"]; !ok {
		t.Error("runningByName: expected 'gemma:7b' to be mapped")
	}
	if len(m) != 2 {
		t.Errorf("runningByName returned map with size %d; want 2", len(m))
	}
}
