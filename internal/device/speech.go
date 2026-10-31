package device

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/engine"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// audioInfo describes a synthesized reply after it was written to disk.
type audioInfo struct {
	Path     string
	MIME     string
	Bytes    int
	Rate     int
	Channels int
	Bits     int
}

// synthesizeReply speaks the assistant answer with the speech role model and
// registers the file on the assistant message, so the web UI can play exactly
// what the device heard. Text is cleaned and capped to DEVICE_REPLY_MAX_CHARS:
// the rest of a long answer stays readable in the session.
func (c *Channel) synthesizeReply(ctx context.Context, cfg config.Config, deps Deps, reply engine.FinalReplyContext, turnID string) (audioInfo, error) {
	model := config.ResolveModel(cfg, config.ModelRoleSpeech)
	if strings.TrimSpace(model) == "" {
		return audioInfo{}, errors.New("no speech model configured")
	}
	if deps.Client == nil {
		return audioInfo{}, errors.New("ollama client is required")
	}
	if deps.SessionStore == nil {
		return audioInfo{}, errors.New("session store is required")
	}
	text := tools.PrepareVoiceText(reply.Text, cfg.DeviceReplyMaxChars)
	if text == "" {
		return audioInfo{}, errors.New("reply has no speakable text")
	}

	// A cold speech model may need to load before the first sample; give the
	// synthesis its own budget so a long turn does not eat it.
	speakCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := deps.Client.Speech(speakCtx, ollama.SpeechRequest{
		Model:          model,
		Input:          text,
		Voice:          cfg.OllamaSpeechVoice,
		ResponseFormat: ollama.SpeechFormatWAV,
	})
	if err != nil {
		return audioInfo{}, fmt.Errorf("speech synthesis: %w", err)
	}
	if len(res.Audio) == 0 {
		return audioInfo{}, errors.New("speech model returned no audio")
	}

	dir := filepath.Join(deps.SessionStore.Dir(), reply.SessionID, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return audioInfo{}, fmt.Errorf("create attachments dir: %w", err)
	}
	filename := "device_reply_" + turnID + ".wav"
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, res.Audio, 0o644); err != nil {
		return audioInfo{}, fmt.Errorf("save spoken reply: %w", err)
	}
	mime := res.ContentType
	if strings.TrimSpace(mime) == "" {
		mime = "audio/wav"
	}
	if reply.Recorder != nil {
		reply.Recorder.AddAttachmentRefWithTranscription(filename, mime, path, text)
	}
	rate, channels, bits := wavFormat(res.Audio)
	return audioInfo{
		Path:     path,
		MIME:     mime,
		Bytes:    len(res.Audio),
		Rate:     rate,
		Channels: channels,
		Bits:     bits,
	}, nil
}

// handleTurnAudio serves the spoken reply. It supports Range requests and HEAD
// through ServeContent, and ?format=pcm for devices that stream raw samples.
func (c *Channel) handleTurnAudio(w http.ResponseWriter, r *http.Request) {
	deviceID, err := deviceID(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	if !c.turnBelongsTo(id, deviceID) {
		writeAPIError(w, http.StatusNotFound, "turn not found")
		return
	}
	view, ok := c.turns.view(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "turn not found")
		return
	}
	if !view.AudioReady {
		writeAPIError(w, http.StatusConflict, "reply audio is not ready")
		return
	}
	data, err := os.ReadFile(view.audioPath)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "reply audio is unavailable")
		return
	}

	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Device-Session", view.SessionID)
	setAudioHeaders(w, view)

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "pcm") {
		w.Header().Set("Content-Type", "audio/pcm")
		http.ServeContent(w, r, id+".pcm", time.Time{}, bytes.NewReader(wavData(data)))
		return
	}
	w.Header().Set("Content-Type", view.AudioMIME)
	http.ServeContent(w, r, id+".wav", time.Time{}, bytes.NewReader(data))
}

func setAudioHeaders(w http.ResponseWriter, view TurnView) {
	if view.AudioFormat != "" {
		w.Header().Set("X-Audio-Format", view.AudioFormat)
	}
	if view.AudioRate > 0 {
		w.Header().Set("X-Audio-Rate", fmt.Sprintf("%d", view.AudioRate))
	}
	if view.AudioChannels > 0 {
		w.Header().Set("X-Audio-Channels", fmt.Sprintf("%d", view.AudioChannels))
	}
	if view.AudioBits > 0 {
		w.Header().Set("X-Audio-Bits", fmt.Sprintf("%d", view.AudioBits))
	}
}

// wavFormat reports the sample rate, channel count and bits per sample of a
// RIFF/WAVE payload. Missing or malformed headers yield zeros.
func wavFormat(data []byte) (rate, channels, bits int) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, 0, 0
	}
	for offset := 12; offset+8 <= len(data); {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if id == "fmt " && body+16 <= len(data) {
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			rate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			return rate, channels, bits
		}
		offset = body + size + (size & 1)
	}
	return 0, 0, 0
}

// wavData returns the samples of a RIFF/WAVE payload without its header. A
// payload that is not WAV is returned unchanged, so raw PCM still works.
func wavData(data []byte) []byte {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return data
	}
	for offset := 12; offset+8 <= len(data); {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if id == "data" {
			end := body + size
			if size <= 0 || end > len(data) {
				end = len(data)
			}
			return data[body:end]
		}
		offset = body + size + (size & 1)
	}
	return data
}
