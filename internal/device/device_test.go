package device

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func testChannel(cfg config.Config) *Channel {
	mgr := config.NewManager(cfg)
	client := ollama.NewClient("http://127.0.0.1:1")
	return NewChannel(func() Deps {
		return Deps{ConfigMgr: mgr, Client: client}
	})
}

func deviceMux(c *Channel) *http.ServeMux {
	mux := http.NewServeMux()
	c.Mount(mux)
	return mux
}

func TestHealthAuthentication(t *testing.T) {
	base := config.Config{
		DeviceEnabled:      true,
		DeviceToken:        "secret-token",
		OllamaDefaultModel: "main-model",
		OllamaModelAudio:   "audio-model",
		OllamaModelSpeech:  "speech-model",
	}
	tests := []struct {
		name       string
		configure  func(*config.Config)
		token      string
		wantStatus int
	}{
		{
			name:       "disabled channel is not advertised",
			configure:  func(cfg *config.Config) { cfg.DeviceEnabled = false },
			token:      "secret-token",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "missing token is rejected",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong token is rejected",
			token:      "nope",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "configured token is accepted",
			token:      "secret-token",
			wantStatus: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			if tt.configure != nil {
				tt.configure(&cfg)
			}
			c := testChannel(cfg)
			req := httptest.NewRequest(http.MethodGet, "/api/device/health", nil)
			if tt.token != "" {
				req.Header.Set("X-Device-Token", tt.token)
			}
			rec := httptest.NewRecorder()
			deviceMux(c).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestHealthReportsCapabilities(t *testing.T) {
	tests := []struct {
		name           string
		audioModel     string
		speechModel    string
		wantOK         bool
		wantTranscribe bool
		wantSpeak      bool
	}{
		{
			name:           "dedicated audio and speech models",
			audioModel:     "audio-model",
			speechModel:    "speech-model",
			wantOK:         true,
			wantTranscribe: true,
			wantSpeak:      true,
		},
		{
			name:           "speech only cannot answer audio input",
			speechModel:    "speech-model",
			wantTranscribe: false,
			wantSpeak:      true,
		},
		{
			name:           "audio only cannot speak",
			audioModel:     "audio-model",
			wantTranscribe: true,
			wantSpeak:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testChannel(config.Config{
				DeviceEnabled:      true,
				DeviceToken:        "secret-token",
				OllamaDefaultModel: "main-model",
				OllamaModelAudio:   tt.audioModel,
				OllamaModelSpeech:  tt.speechModel,
			})
			req := httptest.NewRequest(http.MethodGet, "/api/device/health", nil)
			req.Header.Set("Authorization", "Bearer secret-token")
			rec := httptest.NewRecorder()
			deviceMux(c).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			var got healthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.CanTranscribe != tt.wantTranscribe {
				t.Fatalf("can_transcribe = %v, want %v", got.CanTranscribe, tt.wantTranscribe)
			}
			if got.CanSpeak != tt.wantSpeak {
				t.Fatalf("can_speak = %v, want %v", got.CanSpeak, tt.wantSpeak)
			}
			if got.OK != tt.wantOK {
				t.Fatalf("ok = %v, want %v (error %q)", got.OK, tt.wantOK, got.Error)
			}
		})
	}
}

func TestValidateRejectsHalfConfiguredChannel(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		client  *ollama.Client
		wantErr string
	}{
		{
			name: "disabled channel is always valid",
			cfg:  config.Config{DeviceEnabled: false},
		},
		{
			name: "missing token",
			cfg: config.Config{
				DeviceEnabled:      true,
				OllamaDefaultModel: "main-model",
				OllamaModelSpeech:  "speech-model",
			},
			wantErr: "DEVICE_TOKEN",
		},
		{
			name: "missing speech model",
			cfg: config.Config{
				DeviceEnabled:      true,
				DeviceToken:        "secret-token",
				OllamaDefaultModel: "main-model",
				OllamaModelAudio:   "audio-model",
			},
			wantErr: "OLLAMA_MODEL_SPEECH",
		},
		{
			name: "missing transcription support",
			cfg: config.Config{
				DeviceEnabled:      true,
				DeviceToken:        "secret-token",
				OllamaDefaultModel: "main-model",
				OllamaModelSpeech:  "speech-model",
			},
			wantErr: "transcribe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := config.NewManager(tt.cfg)
			c := NewChannel(func() Deps {
				return Deps{ConfigMgr: mgr, Client: ollama.NewClient("http://127.0.0.1:1")}
			})
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErr)) {
				t.Fatalf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestDeviceIDValidation(t *testing.T) {
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "simple id", value: "kitchen-1", ok: true},
		{name: "underscore id", value: "desk_unit", ok: true},
		{name: "empty", value: ""},
		{name: "spaces", value: "bad id"},
		{name: "too long", value: strings.Repeat("a", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.value != "" {
				req.Header.Set("X-Device-Id", tt.value)
			}
			_, err := deviceID(req)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
