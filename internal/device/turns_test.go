package device

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

const (
	testDeviceToken = "test-device-token"
	testDeviceID    = "kitchen-1"
)

// fakeOllama answers the two calls a device turn makes: the structured audio
// transcription and the streaming agent reply. A non-nil gate holds the
// streaming reply until the test releases it.
func fakeOllama(t *testing.T, answer string, gate <-chan struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Stream == nil || !*req.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Message: ollama.Message{Role: "assistant", Content: `{"transcription":"turn on the kitchen light"}`},
				Done:    true,
			})
			return
		}
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
			Message: ollama.Message{Role: "assistant", Content: answer},
		})
		_ = json.NewEncoder(w).Encode(ollama.ChatResponse{Done: true})
	}))
}

func newDeviceHarness(t *testing.T, gate <-chan struct{}) (*http.ServeMux, *sessions.Store, config.Config) {
	t.Helper()
	server := fakeOllama(t, "The kitchen light is on.", gate)
	t.Cleanup(server.Close)
	cfg := config.Config{
		OllamaBaseURL:        server.URL,
		OllamaDefaultModel:   "main-model",
		OllamaModelAudio:     "audio-model",
		OllamaModelSpeech:    "speech-model",
		DeviceEnabled:        true,
		DeviceToken:          testDeviceToken,
		DeviceMaxAudioMB:     1,
		DeviceTurnTimeoutMin: 1,
		DeviceReplyMaxChars:  200,
		Workspace:            t.TempDir(),
		SessionsPath:         t.TempDir(),
		MemoryPath:           t.TempDir(),
		SkillsPath:           t.TempDir(),
	}
	client := ollama.NewClient(server.URL)
	store := sessions.NewStore(cfg.SessionsPath)
	mem := memory.NewStore(cfg.MemoryPath)
	channel := NewChannel(func() Deps {
		return Deps{
			ConfigMgr:    config.NewManager(cfg),
			Client:       client,
			SessionStore: store,
			MemoryStore:  mem,
			CachePath:    filepath.Join(t.TempDir(), "cache.json"),
		}
	})
	t.Cleanup(func() { channel.turns.cancelAll(2 * time.Second) })
	return deviceMux(channel), store, cfg
}

func postAudio(t *testing.T, mux *http.ServeMux, deviceID, contentType string, interrupt bool) *httptest.ResponseRecorder {
	t.Helper()
	body := bytes.Repeat([]byte{0x01}, 512)
	req := httptest.NewRequest(http.MethodPost, "/api/device/messages", bytes.NewReader(body))
	req.Header.Set("X-Device-Token", testDeviceToken)
	req.Header.Set("X-Device-Id", deviceID)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if interrupt {
		req.Header.Set("X-Device-Interrupt", "1")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func getTurn(t *testing.T, mux *http.ServeMux, deviceID, turnID string) TurnView {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/device/turns/"+turnID, nil)
	req.Header.Set("X-Device-Token", testDeviceToken)
	req.Header.Set("X-Device-Id", deviceID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status request returned %d: %s", rec.Code, rec.Body.String())
	}
	var view TurnView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func waitForTurnStatus(t *testing.T, mux *http.ServeMux, deviceID, turnID string, want ...string) TurnView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view := getTurn(t, mux, deviceID, turnID)
		for _, status := range want {
			if view.Status == status {
				return view
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("turn %s never reached %v", turnID, want)
	return TurnView{}
}

func TestTurnCompletesAndStoresSession(t *testing.T) {
	mux, store, _ := newDeviceHarness(t, nil)
	rec := postAudio(t, mux, testDeviceID, "audio/wav", false)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created createTurnResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.TurnID == "" || created.SessionID == "" {
		t.Fatalf("incomplete response: %+v", created)
	}

	view := waitForTurnStatus(t, mux, testDeviceID, created.TurnID, StatusReady, StatusFailed)
	if view.Status != StatusReady {
		t.Fatalf("turn failed: %s", view.Error)
	}
	if view.Transcript != "turn on the kitchen light" {
		t.Fatalf("transcript = %q", view.Transcript)
	}
	if view.DisplayText != "The kitchen light is on." {
		t.Fatalf("display_text = %q", view.DisplayText)
	}
	if view.SessionID != created.SessionID {
		t.Fatalf("session id changed: %q vs %q", view.SessionID, created.SessionID)
	}

	sess, err := store.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Title != "Device "+testDeviceID {
		t.Fatalf("session title = %q", sess.Title)
	}

	// The next turn continues the same conversation.
	rec = postAudio(t, mux, testDeviceID, "audio/wav", false)
	var second createTurnResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	if second.SessionID != created.SessionID {
		t.Fatalf("second turn moved to session %q", second.SessionID)
	}
	waitForTurnStatus(t, mux, testDeviceID, second.TurnID, StatusReady, StatusFailed)
}

func TestBusyDeviceGetsConflictUntilCancel(t *testing.T) {
	gate := make(chan struct{})
	mux, _, _ := newDeviceHarness(t, gate)
	first := postAudio(t, mux, testDeviceID, "audio/wav", false)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body %s", first.Code, first.Body.String())
	}
	var created createTurnResponse
	_ = json.Unmarshal(first.Body.Bytes(), &created)

	conflict := postAudio(t, mux, testDeviceID, "audio/wav", false)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("concurrent status = %d, want 409", conflict.Code)
	}
	var active TurnView
	if err := json.Unmarshal(conflict.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if active.ID != created.TurnID {
		t.Fatalf("conflict reported turn %q, want %q", active.ID, created.TurnID)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/device/turns/"+created.TurnID+"/cancel", nil)
	cancelReq.Header.Set("X-Device-Token", testDeviceToken)
	cancelReq.Header.Set("X-Device-Id", testDeviceID)
	cancelRec := httptest.NewRecorder()
	mux.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body %s", cancelRec.Code, cancelRec.Body.String())
	}
	waitForTurnStatus(t, mux, testDeviceID, created.TurnID, StatusCanceled, StatusFailed)

	close(gate)
	next := postAudio(t, mux, testDeviceID, "audio/wav", false)
	if next.Code != http.StatusAccepted {
		t.Fatalf("post after cancel status = %d, body %s", next.Code, next.Body.String())
	}
}

func TestTurnIsScopedToItsDevice(t *testing.T) {
	mux, _, _ := newDeviceHarness(t, nil)
	rec := postAudio(t, mux, testDeviceID, "audio/wav", false)
	var created createTurnResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	req := httptest.NewRequest(http.MethodGet, "/api/device/turns/"+created.TurnID, nil)
	req.Header.Set("X-Device-Token", testDeviceToken)
	req.Header.Set("X-Device-Id", "other-device")
	other := httptest.NewRecorder()
	mux.ServeHTTP(other, req)
	if other.Code != http.StatusNotFound {
		t.Fatalf("foreign device status = %d, want 404", other.Code)
	}
}

func TestNewSessionRelinksDevice(t *testing.T) {
	mux, _, cfg := newDeviceHarness(t, nil)
	first := postAudio(t, mux, testDeviceID, "audio/wav", false)
	var created createTurnResponse
	_ = json.Unmarshal(first.Body.Bytes(), &created)

	req := httptest.NewRequest(http.MethodPost, "/api/device/sessions", nil)
	req.Header.Set("X-Device-Token", testDeviceToken)
	req.Header.Set("X-Device-Id", testDeviceID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created2 newSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created2); err != nil {
		t.Fatal(err)
	}
	if created2.SessionID == "" || created2.SessionID == created.SessionID {
		t.Fatalf("new session id = %q (old %q)", created2.SessionID, created.SessionID)
	}

	links := loadSessionLinks(filepath.Join(cfg.SessionsPath, deviceLinksFile))
	if got := links.Get(testDeviceID); got != created2.SessionID {
		t.Fatalf("linked session = %q, want %q", got, created2.SessionID)
	}
}

func TestRejectsUnsupportedContentType(t *testing.T) {
	mux, _, _ := newDeviceHarness(t, nil)
	rec := postAudio(t, mux, testDeviceID, "text/plain", false)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestScreenTextStripsMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "headings and emphasis", in: "## Done\n**Bold** text", want: "Done\nBold text"},
		{name: "links", in: "See [the docs](https://example.com/docs) now", want: "See the docs now"},
		{name: "code", in: "Run `ls -la` in ```sh\nls\n```", want: "Run ls -la in ls"},
		{name: "truncates on word boundary", in: "one two three four five", want: "one two three…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := 180
			if tt.name == "truncates on word boundary" {
				limit = 15
			}
			if got := screenText(tt.in, limit); got != tt.want {
				t.Fatalf("screenText = %q, want %q", got, tt.want)
			}
		})
	}
}
