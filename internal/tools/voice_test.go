package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type fakeAttachmentHandler struct {
	ref  string
	mime string
	path string
	call int
}

func (f *fakeAttachmentHandler) OnAttachmentGenerated(sessionID string, ref string, mime string, path string, transcription string) {
	f.call++
	f.ref = ref
	f.mime = mime
	f.path = path
}

func newVoiceRegistry(t *testing.T, model string, handler AttachmentGeneratedHandler) (*Registry, string) {
	t.Helper()
	workspace := t.TempDir()
	sessionsPath := filepath.Join(workspace, "sessions")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollama.SpeechRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFFfakewavdata"))
	}))
	t.Cleanup(server.Close)

	registry := NewRegistry(false, workspace, nil, ollama.NewClient(server.URL), "", SearchConfig{})
	registry.SetSessionsPath(sessionsPath)
	registry.SetSessionID("sess-1")
	registry.SetAttachmentGeneratedHandler(handler)
	registry.SetSpeechModel(model)
	return registry, sessionsPath
}

func callVoice(t *testing.T, registry *Registry, text string) (string, error) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"text": text})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return registry.Execute(context.Background(), ollama.ToolCall{
		Type:     "function",
		Function: ollama.ToolFunction{Name: "send_voice", Arguments: payload},
	})
}

func TestSetSpeechModelControlsToolVisibility(t *testing.T) {
	registry, _ := newVoiceRegistry(t, "", &fakeAttachmentHandler{})
	if hasTool(registry, "send_voice") {
		t.Fatal("send_voice should be hidden without a speech model")
	}
	if _, err := callVoice(t, registry, "Hola"); err == nil {
		t.Fatal("expected send_voice to be rejected without a speech model")
	}

	registry.SetSpeechModel("phonon:40m-es")
	if !hasTool(registry, "send_voice") {
		t.Fatal("send_voice should be exposed once a speech model is set")
	}

	registry.SetSpeechModel("   ")
	if hasTool(registry, "send_voice") {
		t.Fatal("send_voice should be removed when the speech model is cleared")
	}
}

func TestSetSpeechModelIsIdempotent(t *testing.T) {
	registry, _ := newVoiceRegistry(t, "phonon", &fakeAttachmentHandler{})
	registry.SetSpeechModel("phonon")
	count := 0
	for _, def := range registry.Definitions() {
		if def.Function.Name == "send_voice" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("send_voice defined %d times, want 1", count)
	}
}

func TestSetSpeechVoice(t *testing.T) {
	registry := NewRegistry(false, t.TempDir(), nil, nil, "", SearchConfig{})
	if registry.SpeechVoice() != "" {
		t.Fatalf("expected empty default voice, got %q", registry.SpeechVoice())
	}
	registry.SetSpeechVoice("Zoey")
	if registry.SpeechVoice() != "Zoey" {
		t.Fatalf("expected voice Zoey, got %q", registry.SpeechVoice())
	}
	registry.SetSpeechVoice("  Freya  ")
	if registry.SpeechVoice() != "Freya" {
		t.Fatalf("expected trimmed voice Freya, got %q", registry.SpeechVoice())
	}
}

func TestSendVoiceSavesAttachmentAndReportsDelivery(t *testing.T) {
	handler := &fakeAttachmentHandler{}
	registry, sessionsPath := newVoiceRegistry(t, "phonon:40m-es", handler)

	result, err := callVoice(t, registry, "Hola, ¿cómo estás?")
	if err != nil {
		t.Fatalf("send_voice: %v", err)
	}
	if !strings.Contains(result, "Voice note delivered") {
		t.Fatalf("unexpected result: %q", result)
	}
	if handler.call != 1 {
		t.Fatalf("attachment handler called %d times, want 1", handler.call)
	}
	if handler.mime != "audio/wav" {
		t.Fatalf("mime = %q, want audio/wav", handler.mime)
	}
	if !strings.HasPrefix(handler.ref, "voice_") || !strings.HasSuffix(handler.ref, ".wav") {
		t.Fatalf("unexpected attachment name: %q", handler.ref)
	}
	wantDir := filepath.Join(sessionsPath, "sess-1", "attachments")
	if filepath.Dir(handler.path) != wantDir {
		t.Fatalf("attachment path = %q, want inside %q", handler.path, wantDir)
	}
	written, err := os.ReadFile(handler.path)
	if err != nil {
		t.Fatalf("read voice file: %v", err)
	}
	if string(written) != "RIFFfakewavdata" {
		t.Fatalf("unexpected audio contents: %q", string(written))
	}
}

func TestSendVoiceStripsMarkdownAndCode(t *testing.T) {
	var spoken string
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollama.SpeechRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		spoken = req.Input
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFFfakewavdata"))
	}))
	defer server.Close()

	registry := NewRegistry(false, workspace, nil, ollama.NewClient(server.URL), "", SearchConfig{})
	registry.SetSessionsPath(filepath.Join(workspace, "sessions"))
	registry.SetSessionID("sess-1")
	registry.SetAttachmentGeneratedHandler(&fakeAttachmentHandler{})
	registry.SetSpeechModel("phonon")

	input := "**Listo**: revisé [el repo](https://example.com/x) y funciona.\n\n```go\nfmt.Println(\"no\")\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |"
	if _, err := callVoice(t, registry, input); err != nil {
		t.Fatalf("send_voice: %v", err)
	}
	if strings.Contains(spoken, "```") || strings.Contains(spoken, "https://") || strings.Contains(spoken, "**") || strings.Contains(spoken, "|") {
		t.Fatalf("spoken text still carries markup: %q", spoken)
	}
	if !strings.Contains(spoken, "revisé el repo y funciona") {
		t.Fatalf("spoken text lost the sentence: %q", spoken)
	}
}

func TestSendVoiceRejectsEmptyAndTooLongText(t *testing.T) {
	handler := &fakeAttachmentHandler{}
	registry, _ := newVoiceRegistry(t, "phonon", handler)

	if _, err := callVoice(t, registry, "```\nonly code\n```"); err == nil {
		t.Fatal("expected an error when the text is only code")
	}
	if _, err := callVoice(t, registry, strings.Repeat("a", voiceMaxChars+1)); err == nil {
		t.Fatal("expected an error for text over the limit")
	}
	if handler.call != 0 {
		t.Fatalf("attachment handler should not be called, got %d", handler.call)
	}
}

func TestSendVoiceRequiresAttachmentChannel(t *testing.T) {
	registry, _ := newVoiceRegistry(t, "phonon", nil)
	if _, err := callVoice(t, registry, "Hola"); err == nil {
		t.Fatal("expected an error without an attachment handler")
	}
}

func TestSendVoiceAllowsNewWordsBlocksDuplicates(t *testing.T) {
	handler := &fakeAttachmentHandler{}
	registry, _ := newVoiceRegistry(t, "phonon:40m-es", handler)

	first, err := callVoice(t, registry, "Hola, todo listo.")
	if err != nil {
		t.Fatalf("first send_voice: %v", err)
	}
	if !strings.Contains(first, "Voice note delivered") {
		t.Fatalf("unexpected first result: %q", first)
	}

	// Same words in a different casing get blocked without new audio.
	second, err := callVoice(t, registry, "HOLA, TODO LISTO.")
	if err != nil {
		t.Fatalf("duplicate send_voice should not error: %v", err)
	}
	if !strings.Contains(second, "already sent this voice message") {
		t.Fatalf("duplicate call should warn without new audio, got: %q", second)
	}
	if handler.call != 1 {
		t.Fatalf("attachment handler called %d times, want 1", handler.call)
	}

	// Same words with different punctuation ("¡!" placement) also block:
	// this is the near-duplicate bypass that delivered the same voice twice.
	third, err := callVoice(t, registry, "¡HOLA, TODO LISTO!")
	if err != nil {
		t.Fatalf("near-duplicate send_voice should not error: %v", err)
	}
	if !strings.Contains(third, "already sent this voice message") {
		t.Fatalf("near-duplicate call should warn without new audio, got: %q", third)
	}
	if handler.call != 1 {
		t.Fatalf("attachment handler called %d times, want 1", handler.call)
	}

	// Distinct words are a new voice note and synthesize again.
	fourth, err := callVoice(t, registry, "Ya encontré el archivo.")
	if err != nil {
		t.Fatalf("second distinct send_voice: %v", err)
	}
	if !strings.Contains(fourth, "Voice note delivered") {
		t.Fatalf("unexpected fourth result: %q", fourth)
	}
	if handler.call != 2 {
		t.Fatalf("attachment handler called %d times, want 2", handler.call)
	}
}

func TestSendVoiceBlocksRealNearDuplicate(t *testing.T) {
	handler := &fakeAttachmentHandler{}
	registry, _ := newVoiceRegistry(t, "phonon:40m-es", handler)

	// Transcriptions from the reported double-delivery: same message, only
	// "¡!" placement differs.
	first := "¡Claro que sí! Revisando el pronóstico detallado, el día más caluroso de la semana será el jueves. Es el día que se espera que alcance los 32 grados Celsius, ¡así que ese será el día para disfrutar de un sol radiante y un calorcito intenso! También hay otros días, como el miércoles y el viernes, donde se esperan temperaturas muy cercanas a los 30 o 31 grados, pero el jueves es el campeón de la semana. ¡A disfrutar de ese calor!"
	second := "Claro que sí! Revisando el pronóstico detallado, el día más caluroso de la semana será el jueves. Es el día que se espera que alcance los 32 grados Celsius, así que ese será el día para disfrutar de un sol radiante y un calorcito intenso! También hay otros días, como el miércoles y el viernes, donde se esperan temperaturas muy cercanas a los 30 o 31 grados, pero el jueves es el campeón de la semana. ¡A disfrutar de ese calor!"

	if _, err := callVoice(t, registry, first); err != nil {
		t.Fatalf("first send_voice: %v", err)
	}
	res, err := callVoice(t, registry, second)
	if err != nil {
		t.Fatalf("near-duplicate send_voice should not error: %v", err)
	}
	if !strings.Contains(res, "already sent this voice message") {
		t.Fatalf("near-duplicate should be blocked without new audio, got: %q", res)
	}
	if handler.call != 1 {
		t.Fatalf("attachment handler called %d times, want 1", handler.call)
	}
}

func TestCleanSpeechText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "Hola, todo bien.", want: "Hola, todo bien."},
		{name: "heading and bold", input: "## Resumen\n**Todo listo**", want: "Resumen Todo listo"},
		{name: "bullets", input: "- uno\n- dos", want: "uno dos"},
		{name: "numbered", input: "1. uno\n2. dos", want: "uno dos"},
		{name: "link", input: "mirá [esto](https://x.y) ahora", want: "mirá esto ahora"},
		{name: "bare url", input: "abrí https://example.com/a?b=1 ya", want: "abrí ya"},
		{name: "code block", input: "antes\n```py\nprint(1)\n```\ndespués", want: "antes después"},
		{name: "inline code", input: "usá `npm test` ahora", want: "usá npm test ahora"},
		{name: "table", input: "| a | b |\n|---|---|\n| 1 | 2 |", want: ""},
		{name: "blank", input: "   \n  ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanSpeechText(tt.input); got != tt.want {
				t.Fatalf("cleanSpeechText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func hasTool(registry *Registry, name string) bool {
	for _, def := range registry.Definitions() {
		if def.Function.Name == name {
			return true
		}
	}
	return false
}
