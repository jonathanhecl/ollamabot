package probe

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// fakeWAV builds a 44-byte header plus payload, mimicking Ollama's streamed
// WAV where the RIFF sizes hold placeholder maximum values.
func fakeWAV(payloadBytes int) []byte {
	buf := make([]byte, 44+payloadBytes)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], ^uint32(0))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], 24000)
	binary.LittleEndian.PutUint32(buf[28:32], 48000)
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], ^uint32(0))
	return buf
}

func newSpeechServer(capabilitiesList []string, voices []string, audio []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				Capabilities: capabilitiesList,
				Voices:       voices,
				Details:      ollama.ModelDetails{Family: "phonon"},
			})
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(audio)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSpeechProbeConfirmsSpeechModel(t *testing.T) {
	server := newSpeechServer([]string{"speech"}, []string{"default"}, fakeWAV(48000))
	defer server.Close()

	runner := NewRunner(ollama.NewClient(server.URL))
	res, err := runner.Speech(context.Background(), "phonon:40m-es", "")
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if res.Status != capabilities.Confirmed {
		t.Fatalf("status = %q, want confirmed (details: %s)", res.Status, res.Details)
	}
	if !strings.Contains(res.Details, "voices: default") {
		t.Fatalf("details should list voices, got %q", res.Details)
	}
	if !strings.Contains(res.Details, "about 1.0s") {
		t.Fatalf("details should estimate duration from payload size, got %q", res.Details)
	}
}

func TestSpeechProbeWithoutVoicesField(t *testing.T) {
	server := newSpeechServer([]string{"speech"}, nil, fakeWAV(48000))
	defer server.Close()

	runner := NewRunner(ollama.NewClient(server.URL))
	res, err := runner.Speech(context.Background(), "phonon:40m-es", "")
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if !strings.Contains(res.Details, "cannot be listed") {
		t.Fatalf("details should explain the missing voices field, got %q", res.Details)
	}
}

func TestSpeechProbeRejectsNonSpeechModel(t *testing.T) {
	server := newSpeechServer([]string{"completion", "tools"}, nil, fakeWAV(48))
	defer server.Close()

	runner := NewRunner(ollama.NewClient(server.URL))
	res, err := runner.Speech(context.Background(), "gemma4", "")
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if res.Status != capabilities.Pending {
		t.Fatalf("status = %q, want pending", res.Status)
	}
	if !strings.Contains(res.Details, "speech capability") {
		t.Fatalf("unexpected details: %q", res.Details)
	}
}

func TestSpeechProbeRejectsNonWAVBody(t *testing.T) {
	server := newSpeechServer([]string{"speech"}, nil, []byte("not audio at all"))
	defer server.Close()

	runner := NewRunner(ollama.NewClient(server.URL))
	res, err := runner.Speech(context.Background(), "phonon", "")
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if res.Status != capabilities.Pending || !strings.Contains(res.Details, "not a WAV") {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestSpeechProbeWritesAudioFile(t *testing.T) {
	audio := fakeWAV(48000)
	server := newSpeechServer([]string{"speech"}, []string{"default"}, audio)
	defer server.Close()

	out := filepath.Join(t.TempDir(), "probe.wav")
	runner := NewRunner(ollama.NewClient(server.URL))
	res, err := runner.Speech(context.Background(), "phonon", out)
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if res.Status != capabilities.Confirmed {
		t.Fatalf("status = %q, want confirmed", res.Status)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if len(written) != len(audio) {
		t.Fatalf("written %d bytes, want %d", len(written), len(audio))
	}
}

func TestSpeechProbeRequiresModel(t *testing.T) {
	runner := NewRunner(ollama.NewClient("http://127.0.0.1:1"))
	if _, err := runner.Speech(context.Background(), "  ", ""); err == nil {
		t.Fatal("expected an error for an empty model name")
	}
}
