package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
)

func TestIsVoiceContainer(t *testing.T) {
	tests := map[string]bool{
		"note.ogg":  true,
		"note.OGG":  true,
		"note.opus": true,
		"note.mp3":  true,
		"note.m4a":  true,
		"note.wav":  false,
		"note.pcm":  false,
		"note":      false,
	}
	for path, want := range tests {
		if got := isVoiceContainer(path); got != want {
			t.Fatalf("isVoiceContainer(%q) = %v, want %v", path, got, want)
		}
	}
}

// newVoiceTestBot wires a Bot against a fake Telegram API so delivery can be
// asserted without network access.
func newVoiceTestBot(t *testing.T, workspace string) (*Bot, *[]string, *[]string) {
	t.Helper()
	var paths []string
	var fields []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			for name := range r.MultipartForm.File {
				fields = append(fields, name)
			}
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
	}))
	t.Cleanup(server.Close)

	mgr := config.NewManager(config.Config{Workspace: workspace})
	bot := &Bot{
		cfg:        mgr,
		apiBase:    server.URL + "/botTEST",
		httpClient: server.Client(),
		approvals:  map[string]chan bool{},
	}
	return bot, &paths, &fields
}

func TestAttachmentHandlerSendsVoiceForOGG(t *testing.T) {
	workspace := t.TempDir()
	bot, paths, fields := newVoiceTestBot(t, workspace)
	voicePath := filepath.Join(workspace, "voice_1.ogg")
	if err := os.WriteFile(voicePath, []byte("OggSfake"), 0o644); err != nil {
		t.Fatalf("write ogg: %v", err)
	}

	handler := &telegramAttachmentHandler{bot: bot, chatID: 42}
	handler.OnAttachmentGenerated("sess-1", "voice_1.ogg", "audio/ogg", voicePath)

	if len(*paths) != 1 || (*paths)[0] != "/botTEST/sendVoice" {
		t.Fatalf("telegram calls = %v, want one sendVoice", *paths)
	}
	if len(*fields) != 1 || (*fields)[0] != "voice" {
		t.Fatalf("multipart fields = %v, want [voice]", *fields)
	}
}

func TestAttachmentHandlerFallsBackToDocumentWithoutFFmpeg(t *testing.T) {
	workspace := t.TempDir()
	bot, paths, fields := newVoiceTestBot(t, workspace)
	wavPath := filepath.Join(workspace, "voice_1.wav")
	if err := os.WriteFile(wavPath, []byte("RIFFfakewavdata"), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}

	t.Setenv("PATH", "")
	handler := &telegramAttachmentHandler{bot: bot, chatID: 42}
	handler.OnAttachmentGenerated("sess-1", "voice_1.wav", "audio/wav", wavPath)

	if len(*paths) != 1 || (*paths)[0] != "/botTEST/sendDocument" {
		t.Fatalf("telegram calls = %v, want a document fallback", *paths)
	}
	if len(*fields) != 1 || (*fields)[0] != "document" {
		t.Fatalf("multipart fields = %v, want [document]", *fields)
	}
}

func TestAttachmentHandlerStillSendsPluginFilesAsDocuments(t *testing.T) {
	workspace := t.TempDir()
	bot, paths, _ := newVoiceTestBot(t, workspace)
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	handler := &telegramAttachmentHandler{bot: bot, chatID: 42}
	handler.OnAttachmentGenerated("sess-1", "report.txt", "text/plain", path)

	if len(*paths) != 1 || !strings.HasSuffix((*paths)[0], "/sendDocument") {
		t.Fatalf("telegram calls = %v, want sendDocument", *paths)
	}
}
