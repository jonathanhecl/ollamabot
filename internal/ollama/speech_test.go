package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSpeechSendsRequestAndReturnsAudio(t *testing.T) {
	var received SpeechRequest
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFFfakewavdata"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	res, err := client.Speech(context.Background(), SpeechRequest{
		Model: "phonon:40m-es",
		Input: "Hola, esto es una prueba.",
	})
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if path != "/v1/audio/speech" {
		t.Fatalf("path = %q, want /v1/audio/speech", path)
	}
	if received.Model != "phonon:40m-es" || received.Input != "Hola, esto es una prueba." {
		t.Fatalf("unexpected request payload: %#v", received)
	}
	if received.ResponseFormat != SpeechFormatWAV {
		t.Fatalf("response_format = %q, want %q", received.ResponseFormat, SpeechFormatWAV)
	}
	if received.Voice != "" {
		t.Fatalf("voice = %q, want empty so the checkpoint default is used", received.Voice)
	}
	if string(res.Audio) != "RIFFfakewavdata" {
		t.Fatalf("audio = %q", string(res.Audio))
	}
	if res.ContentType != "audio/wav" || res.Format != SpeechFormatWAV {
		t.Fatalf("unexpected response metadata: %#v", res)
	}
}

func TestSpeechPassesVoiceAndFormat(t *testing.T) {
	var received SpeechRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write([]byte{0x01, 0x02})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	res, err := client.Speech(context.Background(), SpeechRequest{
		Model:          "phonon",
		Input:          "Hola.",
		Voice:          "Zoey",
		ResponseFormat: SpeechFormatPCM,
	})
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if received.Voice != "Zoey" || received.ResponseFormat != SpeechFormatPCM {
		t.Fatalf("unexpected request payload: %#v", received)
	}
	if res.Format != SpeechFormatPCM || res.ContentType != "audio/pcm" {
		t.Fatalf("unexpected response metadata: %#v", res)
	}
}

func TestSpeechSurfacesOllamaErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"response_format \"mp3\" is not supported, use wav or pcm","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Speech(context.Background(), SpeechRequest{Model: "phonon", Input: "Hola."})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "response_format \"mp3\" is not supported") {
		t.Fatalf("error should carry Ollama's message, got: %v", err)
	}
}

func TestSpeechRejectsEmptyInputWithoutCallingServer(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if _, err := client.Speech(context.Background(), SpeechRequest{Model: "phonon", Input: "   "}); err == nil {
		t.Fatal("expected an error for empty input")
	}
	if called {
		t.Fatal("server should not be called for invalid input")
	}
}

func TestSpeechRejectsUnsupportedFormat(t *testing.T) {
	client := NewClient("http://127.0.0.1:1")
	if _, err := client.Speech(context.Background(), SpeechRequest{Model: "phonon", Input: "Hola.", ResponseFormat: "mp3"}); err == nil {
		t.Fatal("expected an error for mp3")
	}
}

func TestSpeechRejectsEmptyAudioBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if _, err := client.Speech(context.Background(), SpeechRequest{Model: "phonon", Input: "Hola."}); err == nil {
		t.Fatal("expected an error for an empty audio body")
	}
}
