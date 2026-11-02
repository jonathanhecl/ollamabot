package device

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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
type fakeOllama struct {
	*httptest.Server
	mu          sync.Mutex
	speechInput string
	speechModel string
}

func (f *fakeOllama) lastSpeech() (input, model string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.speechInput, f.speechModel
}

func fakeOllamaServer(t *testing.T, answer string, gate <-chan struct{}) *fakeOllama {
	t.Helper()
	fake := &fakeOllama{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/audio/speech" {
			var speech ollama.SpeechRequest
			_ = json.NewDecoder(r.Body).Decode(&speech)
			fake.mu.Lock()
			fake.speechInput = speech.Input
			fake.speechModel = speech.Model
			fake.mu.Unlock()
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(testWAV())
			return
		}
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
	return fake
}

func newDeviceHarness(t *testing.T, gate <-chan struct{}) (*http.ServeMux, *sessions.Store, config.Config, *fakeOllama) {
	t.Helper()
	return newDeviceHarnessAnswer(t, "The kitchen light is on.", gate)
}

func newDeviceHarnessAnswer(t *testing.T, answer string, gate <-chan struct{}) (*http.ServeMux, *sessions.Store, config.Config, *fakeOllama) {
	t.Helper()
	server := fakeOllamaServer(t, answer, gate)
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
	return deviceMux(channel), store, cfg, server
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
	mux, store, _, _ := newDeviceHarness(t, nil)
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

	// The uploaded audio is persisted as a real user message with its
	// transcription, so the web timeline renders the same blocks as web and
	// Telegram turns.
	msgs := decodeMessages(t, sess)
	if len(msgs) < 2 {
		t.Fatalf("session has %d messages, want user + assistant", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("first message role = %q, want user", msgs[0].Role)
	}
	if len(msgs[0].Attachments) != 1 {
		t.Fatalf("user message has %d attachments, want 1", len(msgs[0].Attachments))
	}
	audio := msgs[0].Attachments[0]
	if audio.Kind != "audio" || audio.Name == "" {
		t.Fatalf("user attachment = %+v, want a named audio attachment", audio)
	}
	if !strings.HasPrefix(audio.Name, "device_audio_") || !strings.HasSuffix(audio.Name, ".wav") {
		t.Fatalf("user audio name = %q", audio.Name)
	}
	if audio.Mime != "audio/wav" {
		t.Fatalf("user audio mime = %q", audio.Mime)
	}
	if audio.Transcription != "turn on the kitchen light" {
		t.Fatalf("user audio transcription = %q", audio.Transcription)
	}
	if audio.Data == "" || audio.ProcessedBy == "" {
		t.Fatalf("user audio metadata missing: data=%d bytes processed_by=%q", len(audio.Data), audio.ProcessedBy)
	}
	if len(msgs[0].Images) != 1 || msgs[0].Images[0] == "" {
		t.Fatalf("user message kept %d images, want the recording", len(msgs[0].Images))
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("second message role = %q, want assistant", msgs[1].Role)
	}

	// The next turn continues the same conversation.
	rec = postAudio(t, mux, testDeviceID, "audio/wav", false)
	var second createTurnResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	if second.SessionID != created.SessionID {
		t.Fatalf("second turn moved to session %q", second.SessionID)
	}
	waitForTurnStatus(t, mux, testDeviceID, second.TurnID, StatusReady, StatusFailed)

	// Each exchange keeps its own user and assistant blocks; two assistant
	// messages never sit next to each other, or the web UI merges them.
	sess, err = store.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	msgs = decodeMessages(t, sess)
	if len(msgs) != 4 {
		t.Fatalf("session has %d messages after two turns, want 4", len(msgs))
	}
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	for i, want := range wantRoles {
		if msgs[i].Role != want {
			t.Fatalf("message %d role = %q, want %q", i, msgs[i].Role, want)
		}
	}
	if msgs[2].Attachments[0].Transcription != "turn on the kitchen light" {
		t.Fatalf("second turn transcription = %q", msgs[2].Attachments[0].Transcription)
	}
}

func decodeMessages(t *testing.T, sess sessions.Session) []sessions.RawMsg {
	t.Helper()
	out := make([]sessions.RawMsg, 0, len(sess.Messages))
	for _, raw := range sess.Messages {
		var msg sessions.RawMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		out = append(out, msg)
	}
	return out
}

func TestBusyDeviceGetsConflictUntilCancel(t *testing.T) {
	gate := make(chan struct{})
	mux, _, _, _ := newDeviceHarness(t, gate)
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
	mux, _, _, _ := newDeviceHarness(t, nil)
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
	mux, _, cfg, _ := newDeviceHarness(t, nil)
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
	mux, _, _, _ := newDeviceHarness(t, nil)
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

func testPCM() []byte {
	pcm := make([]byte, 64)
	for i := range pcm {
		pcm[i] = byte(i % 8)
	}
	return pcm
}

func testWAV() []byte {
	pcm := testPCM()
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(pcm)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], 16000)
	binary.LittleEndian.PutUint32(header[28:32], 32000)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(pcm)))
	return append(header, pcm...)
}

func getTurnAudio(t *testing.T, mux *http.ServeMux, deviceID, turnID, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/device/turns/"+turnID+"/audio"+query, nil)
	req.Header.Set("X-Device-Token", testDeviceToken)
	req.Header.Set("X-Device-Id", deviceID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestTurnSpeaksReply(t *testing.T) {
	mux, store, _, fake := newDeviceHarness(t, nil)
	rec := postAudio(t, mux, testDeviceID, "audio/wav", false)
	var created createTurnResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	view := waitForTurnStatus(t, mux, testDeviceID, created.TurnID, StatusReady, StatusFailed)
	if view.Status != StatusReady {
		t.Fatalf("turn failed: %s", view.Error)
	}
	if !view.AudioReady {
		t.Fatal("reply audio was not synthesized")
	}
	if view.AudioFormat != "wav" || view.AudioMIME != "audio/wav" {
		t.Fatalf("audio metadata = %q/%q", view.AudioFormat, view.AudioMIME)
	}
	if view.AudioRate != 16000 || view.AudioChannels != 1 || view.AudioBits != 16 {
		t.Fatalf("wav format = %dHz %dch %dbit", view.AudioRate, view.AudioChannels, view.AudioBits)
	}
	if view.AudioURL != "/api/device/turns/"+created.TurnID+"/audio" {
		t.Fatalf("audio_url = %q", view.AudioURL)
	}

	input, model := fake.lastSpeech()
	if model != "speech-model" {
		t.Fatalf("speech model = %q", model)
	}
	if input != "The kitchen light is on." {
		t.Fatalf("spoken text = %q", input)
	}

	wav := getTurnAudio(t, mux, testDeviceID, created.TurnID, "")
	if wav.Code != http.StatusOK {
		t.Fatalf("audio status = %d, body %s", wav.Code, wav.Body.String())
	}
	if wav.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("audio content type = %q", wav.Header().Get("Content-Type"))
	}
	if wav.Header().Get("X-Audio-Rate") != "16000" {
		t.Fatalf("X-Audio-Rate = %q", wav.Header().Get("X-Audio-Rate"))
	}
	if !bytes.HasPrefix(wav.Body.Bytes(), []byte("RIFF")) {
		t.Fatal("audio body is not a WAV file")
	}

	sess, err := store.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	attached := false
	for _, msg := range sess.Messages {
		if strings.Contains(string(msg), "device_reply_") {
			attached = true
		}
	}
	if !attached {
		t.Fatalf("spoken reply was not attached to the session: %s", sess.Messages)
	}

	pcm := getTurnAudio(t, mux, testDeviceID, created.TurnID, "?format=pcm")
	if pcm.Code != http.StatusOK {
		t.Fatalf("pcm status = %d, body %s", pcm.Code, pcm.Body.String())
	}
	if pcm.Header().Get("Content-Type") != "audio/pcm" {
		t.Fatalf("pcm content type = %q", pcm.Header().Get("Content-Type"))
	}
	gotPCM, _ := io.ReadAll(pcm.Body)
	if !bytes.Equal(gotPCM, testPCM()) {
		t.Fatalf("pcm body = %d bytes, want %d", len(gotPCM), len(testPCM()))
	}
}

func TestSpokenReplyIsCappedAndClean(t *testing.T) {
	longAnswer := "**Done.** " + strings.Repeat("This is a long sentence. ", 40)
	mux, _, _, fake := newDeviceHarnessAnswer(t, longAnswer, nil)
	first := postAudio(t, mux, testDeviceID, "audio/wav", false)
	var created createTurnResponse
	_ = json.Unmarshal(first.Body.Bytes(), &created)
	waitForTurnStatus(t, mux, testDeviceID, created.TurnID, StatusReady, StatusFailed)

	input, _ := fake.lastSpeech()
	if strings.Contains(input, "*") || strings.Contains(input, "**") {
		t.Fatalf("spoken text kept markdown: %q", input)
	}
	if utf8.RuneCountInString(input) > 200 {
		t.Fatalf("spoken text has %d runes, want <= 200", utf8.RuneCountInString(input))
	}
	if !strings.HasPrefix(input, "Done.") {
		t.Fatalf("spoken text = %q", input)
	}

	// The full answer remains in the session; only the speech is capped.
	view := getTurn(t, mux, testDeviceID, created.TurnID)
	if len(view.DisplayText) > displayTextLimit+1 {
		t.Fatalf("display_text has %d chars", len(view.DisplayText))
	}
}

func TestWAVHelpers(t *testing.T) {
	rate, channels, bits := wavFormat(testWAV())
	if rate != 16000 || channels != 1 || bits != 16 {
		t.Fatalf("wavFormat = %d/%d/%d", rate, channels, bits)
	}
	if !bytes.Equal(wavData(testWAV()), testPCM()) {
		t.Fatal("wavData did not strip the header")
	}
	if raw := []byte{1, 2, 3}; !bytes.Equal(wavData(raw), raw) {
		t.Fatal("wavData changed a non-WAV payload")
	}
}

func TestNormalizeDeviceTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Device epaper-94a5d71d0470", "Device 94a5d71d0470"},
		{"device EPAPER-kitchen-1", "Device kitchen-1"},
		{"Device epaper", "Device"},
		{"Device kitchen-1", ""},
		{"Telegram Chat (42)", ""},
		{"New session", ""},
	}
	for _, tc := range cases {
		if got := normalizeDeviceTitle(tc.in); got != tc.want {
			t.Fatalf("normalizeDeviceTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMigrateLegacyDeviceTitles(t *testing.T) {
	store := sessions.NewStore(t.TempDir())
	legacy := sessions.Session{ID: sessions.GenerateID(), Title: "Device epaper-94a5d71d0470"}
	current := sessions.Session{ID: sessions.GenerateID(), Title: "Device kitchen-1"}
	other := sessions.Session{ID: sessions.GenerateID(), Title: "Telegram Chat (42)"}
	for _, sess := range []sessions.Session{legacy, current, other} {
		if err := store.Save(sess); err != nil {
			t.Fatal(err)
		}
	}

	channel := NewChannel(func() Deps { return Deps{SessionStore: store} })
	channel.migrateLegacyTitles()
	channel.migrateLegacyTitles() // second run must be a no-op

	if sess, err := store.Get(legacy.ID); err != nil {
		t.Fatal(err)
	} else if sess.Title != "Device 94a5d71d0470" {
		t.Fatalf("legacy title = %q", sess.Title)
	}
	if sess, err := store.Get(current.ID); err != nil {
		t.Fatal(err)
	} else if sess.Title != "Device kitchen-1" {
		t.Fatalf("current title changed to %q", sess.Title)
	}
	if sess, err := store.Get(other.ID); err != nil {
		t.Fatal(err)
	} else if sess.Title != "Telegram Chat (42)" {
		t.Fatalf("unrelated title changed to %q", sess.Title)
	}
}

// stubStreamHandler absorbs agent stream callbacks; turnStageHandler only needs
// to forward to something that accepts them.
type stubStreamHandler struct{}

func (stubStreamHandler) OnThinking(string)                       {}
func (stubStreamHandler) OnContent(string)                        {}
func (stubStreamHandler) OnToolCall(ollama.ToolCall)              {}
func (stubStreamHandler) OnToolStart(string, any, string)         {}
func (stubStreamHandler) OnToolResult(string, string, string)     {}
func (stubStreamHandler) OnMediaPreProcessing(string)             {}
func (stubStreamHandler) OnDone(ollama.ChatResponse)              {}
func (stubStreamHandler) OnEvent(string, any)                     {}
func (stubStreamHandler) OnContextOptimizationStart(int, float64) {}
func (stubStreamHandler) OnContextOptimizationEnd(int, float64, float64) {
}
func (stubStreamHandler) OnContextOptimized([]ollama.Message, string, int) {}

func TestTurnStageHandlerReportsToolWork(t *testing.T) {
	turns := newTurnStore(time.Hour)
	turns.create(&Turn{ID: "t1", Stage: stageThinking})
	handler := &turnStageHandler{
		StreamHandler: stubStreamHandler{},
		update:        func(fn func(*Turn)) { turns.update("t1", fn) },
	}

	handler.OnToolStart("web_search", map[string]any{"query": "clima"}, "internal")
	view, _ := turns.view("t1")
	if view.Stage != stageWorking || view.Activity != "web_search" {
		t.Fatalf("stage after tool start = %q/%q, want working/web_search", view.Stage, view.Activity)
	}

	handler.OnToolResult("web_search", "ok", "internal")
	view, _ = turns.view("t1")
	if view.Stage != stageThinking || view.Activity != "" {
		t.Fatalf("stage after tool result = %q/%q, want thinking/empty", view.Stage, view.Activity)
	}
}

func TestEnsureSessionRenamesLegacyTitle(t *testing.T) {
	store := sessions.NewStore(t.TempDir())
	legacy := sessions.Session{ID: "sess-legacy", Title: "Device epaper-94a5d71d0470"}
	if err := store.Save(legacy); err != nil {
		t.Fatal(err)
	}
	channel := NewChannel(func() Deps { return Deps{SessionStore: store} })
	channel.normalizeSessionTitle(Deps{SessionStore: store}, legacy)

	sess, err := store.Get("sess-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Title != "Device 94a5d71d0470" {
		t.Fatalf("title = %q", sess.Title)
	}
}
