package device

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

// TestSpokenReplyIsNotResentAsImage reproduces the reported failure: the spoken
// reply of a previous turn is stored as a plain .wav next to the session. On
// reload it used to be restored as an image payload on the assistant message,
// so every later turn (even plain text) sent audio to the main model, which
// answered "audio input is not supported".
func TestSpokenReplyIsNotResentAsImage(t *testing.T) {
	dir := t.TempDir()
	store := sessions.NewStore(dir)
	sessionID := "s1"
	if err := store.Save(sessions.Session{ID: sessionID, Title: "Device kitchen"}); err != nil {
		t.Fatal(err)
	}

	attDir := filepath.Join(dir, sessionID, "attachments")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attDir, "device_reply_1.wav"), testWAV(), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs := []sessions.RawMsg{
		{Role: "user", Content: "hola"},
		{
			Role:           "assistant",
			Content:        "Ya llego el Papa a Uruguay.",
			AttachmentRefs: []string{"device_reply_1.wav"},
			Attachments: []sessions.AttachmentMeta{{
				Name: "device_reply_1.wav",
				Mime: "audio/wav",
				Kind: "audio",
			}},
		},
		{Role: "user", Content: "otra cosa"},
	}
	if err := engine.SaveRawMessages(store, sessionID, msgs); err != nil {
		t.Fatal(err)
	}

	// A copied tree plus a fresh store forces a real disk load, which is what a
	// process restart does; the in-memory cache would otherwise hide the bug.
	copied := t.TempDir()
	if err := copyTree(dir, copied); err != nil {
		t.Fatal(err)
	}
	loaded := engine.LoadSessionRawMessages(sessions.NewStore(copied), sessionID)
	for _, m := range loaded {
		if m.Role != "assistant" {
			continue
		}
		if len(m.Images) != 0 {
			t.Fatalf("spoken reply restored as %d image payload(s), kinds=%v", len(m.Images), m.ImageKinds)
		}
		if len(m.Attachments) != 1 || m.Attachments[0].Kind != "audio" {
			t.Fatalf("spoken reply metadata lost: %+v", m.Attachments)
		}
	}

	mr := router.New(nil, router.Config{
		MainModel:   "main-model",
		AudioModel:  "audio-model",
		VisionModel: "vision-model",
	})
	res, err := mr.ResolveMessages(context.Background(), engine.MediaMessagesFromRaw(loaded))
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range res.Messages {
		if len(m.Images) > 0 {
			t.Errorf("message %d (role=%s) carries %d media payload(s) into the main model", i, m.Role, len(m.Images))
		}
	}
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
