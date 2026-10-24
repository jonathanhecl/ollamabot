package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

func streamChunk(w http.ResponseWriter, resp ollama.ChatResponse) {
	payload, _ := json.Marshal(resp)
	_, _ = fmt.Fprintf(w, "%s\n", payload)
}

// TestProcessTurnDeliversVoiceAttachment runs one full turn against a fake
// Ollama host: the model calls send_voice, the audio is synthesized, and the
// session ends up with an audio attachment the channel can deliver.
func TestProcessTurnDeliversVoiceAttachment(t *testing.T) {
	var chatCalls int32
	var spoken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{Capabilities: []string{"completion", "tools"}})
		case "/api/ps":
			_ = json.NewEncoder(w).Encode(ollama.PsResponse{})
		case "/api/chat":
			call := atomic.AddInt32(&chatCalls, 1)
			w.Header().Set("Content-Type", "application/x-ndjson")
			if call == 1 {
				streamChunk(w, ollama.ChatResponse{
					Model: "main-model",
					Message: ollama.Message{
						Role: "assistant",
						ToolCalls: []ollama.ToolCall{{
							Type: "function",
							Function: ollama.ToolFunction{
								Name:      "send_voice",
								Arguments: json.RawMessage(`{"text":"Hola, todo listo."}`),
							},
						}},
					},
				})
				streamChunk(w, ollama.ChatResponse{Model: "main-model", Done: true, DoneReason: "stop"})
				return
			}
			streamChunk(w, ollama.ChatResponse{Model: "main-model", Message: ollama.Message{Role: "assistant", Content: "Listo."}})
			streamChunk(w, ollama.ChatResponse{Model: "main-model", Done: true, DoneReason: "stop"})
		case "/v1/audio/speech":
			var req ollama.SpeechRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			spoken = req.Input
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(append([]byte("RIFF0000WAVE"), make([]byte, 200)...))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	cfg := config.Config{
		OllamaBaseURL:      server.URL,
		OllamaDefaultModel: "main-model",
		OllamaModelSpeech:  "phonon:40m-es",
		Workspace:          workspace,
		SessionsPath:       filepath.Join(workspace, "sessions"),
		MemoryPath:         filepath.Join(workspace, "memory"),
		SkillsPath:         filepath.Join(workspace, "skills"),
	}
	store := sessions.NewStore(cfg.SessionsPath)
	const sessionID = "voice-turn"
	if err := store.Save(sessions.Session{ID: sessionID, Title: "Voice", Model: cfg.OllamaDefaultModel}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	result, err := ProcessTurn(context.Background(), Deps{
		ConfigMgr:    config.NewManager(cfg),
		Client:       ollama.NewClient(server.URL),
		SessionStore: store,
		AttachmentFactory: func(recorder *sessions.Recorder) tools.AttachmentGeneratedHandler {
			return attachmentRecorder{recorder: recorder}
		},
	}, TurnRequest{
		SessionID: sessionID,
		Channel:   "web",
		Messages:  []router.MediaMessage{{Message: ollama.Message{Role: "user", Content: "decime que está listo"}}},
	})
	if err != nil {
		t.Fatalf("ProcessTurn: %v", err)
	}
	if result.FinalAnswer != "Listo." {
		t.Fatalf("final answer = %q, want %q", result.FinalAnswer, "Listo.")
	}
	if spoken != "Hola, todo listo." {
		t.Fatalf("spoken text = %q", spoken)
	}

	sess, err := store.Get(sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	var found bool
	for _, raw := range sess.Messages {
		var msg struct {
			Role        string                    `json:"role"`
			Attachments []sessions.AttachmentMeta `json:"attachments"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		for _, att := range msg.Attachments {
			if att.Kind != "audio" {
				continue
			}
			found = true
			if !strings.HasPrefix(att.Name, "voice_") {
				t.Fatalf("unexpected attachment name: %q", att.Name)
			}
			if _, err := os.Stat(filepath.Join(cfg.SessionsPath, sessionID, "attachments", att.Name)); err != nil {
				t.Fatalf("voice file missing: %v", err)
			}
		}
	}
	if !found {
		for _, raw := range sess.Messages {
			t.Logf("MSG: %s", string(raw))
		}
		t.Fatalf("no audio attachment recorded; messages: %d", len(sess.Messages))
	}
}

type attachmentRecorder struct {
	recorder *sessions.Recorder
}

func (a attachmentRecorder) OnAttachmentGenerated(sessionID string, ref string, mime string, path string) {
	a.recorder.AddAttachmentRef(ref, mime, path)
}
