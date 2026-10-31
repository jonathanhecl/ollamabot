# Device Channel API

The device channel lets a small audio device — an ESP32 with a microphone, a
speaker and a screen — talk to ollamabot audio-to-audio. The device records a
clip, uploads it, polls for progress and downloads a spoken reply.

A device turn is a normal session turn: it runs `engine.ProcessTurn` with its
own conversation, appears in the web UI with its history and its spoken reply,
and uses the same tools, memory and workspace as every other channel. The only
difference is the medium: input and output are audio.

## Requirements

The channel is disabled by default. To enable it:

```dotenv
DEVICE_ENABLED=true
DEVICE_TOKEN=a-long-random-secret
SERVER_ENABLED=true
SERVER_EXPOSE_NETWORK=true
OLLAMA_MODEL_AUDIO=qwen-audio        # or a main model with audio support
OLLAMA_MODEL_SPEECH=phonon          # required: the device only answers with audio
```

| Variable | Default | Meaning |
|----------|---------|---------|
| `DEVICE_ENABLED` | `false` | Mounts `/api/device/` and requires the device configuration |
| `DEVICE_TOKEN` | empty | Shared secret the device sends on every request |
| `DEVICE_MAX_AUDIO_MB` | `15` | Largest accepted upload |
| `DEVICE_TURN_TIMEOUT_MINUTES` | `5` | Per-turn budget before the turn is aborted |
| `DEVICE_REPLY_MAX_CHARS` | `900` | Characters spoken from the reply; the rest stays in the session |

Startup fails when the channel is enabled without a token, a speech model,
transcription support (a dedicated `OLLAMA_MODEL_AUDIO` or a main model the
probe snapshot confirms as audio-capable) or the web server. When
`SERVER_EXPOSE_NETWORK=false` the server binds `127.0.0.1` and a physical device
cannot reach it; the log warns about this at startup.

## Authentication

Every request carries the shared token in `X-Device-Token` or
`Authorization: Bearer <token>`. The token is compared in constant time. Device
routes are exempt from the web password: the device never needs it.

Requests that identify a conversation also send `X-Device-Id`
(`[A-Za-z0-9_-]{1,64}`). The ID maps the physical device to its session in
`sessions/devices.json`; the session survives restarts and is visible in the web
UI as `Device <id>`.

A disabled channel answers `404` so it is not advertised. A bad or missing token
answers `401`.

## Turn lifecycle

```text
queued → processing → speaking → ready
                    ↘ failed
                    ↘ canceled
```

- `processing` — the agent is working. `stage` is `thinking` and becomes
  `heard you` once the audio is transcribed.
- `speaking` — the answer exists and its audio is being synthesized.
- `ready` — the turn is complete. `display_text` holds a short plain-text
  summary for the screen and `audio_url` points at the spoken reply.
- `failed` / `canceled` — `error` explains what happened.

If speech synthesis fails the turn still becomes `ready` with
`audio_ready: false`: the screen keeps the text and the server logs the reason.

## Endpoints

### `GET /api/device/health`

Cheap boot check. Returns `200` even when the channel is not ready.

```json
{
  "ok": true,
  "can_transcribe": true,
  "can_speak": true,
  "transcription_model": "qwen-audio",
  "speech_model": "phonon",
  "session_id": "abc123"
}
```

`session_id` is present only when the request also sends `X-Device-Id` and that
device already has a conversation.

### `POST /api/device/messages`

Starts a turn. The body is the raw audio bytes; `Content-Type` must be one of
`audio/wav`, `audio/x-wav`, `audio/pcm`, `audio/ogg`, `audio/mp3`, `audio/mpeg`
or `audio/mp4` (an absent header is treated as WAV).

Headers:

- `X-Device-Id` (required)
- `X-Device-Token` (required)
- `X-Device-Interrupt: 1` (optional) — cancels the turn in flight and starts this
  one. Without it a second upload while a turn runs answers `409` with the
  current turn JSON.

Response `202`:

```json
{ "turn_id": "turn_123", "session_id": "abc123", "status": "queued" }
```

### `GET /api/device/turns/{turn_id}`

Poll every ~300–500 ms while the device waits.

```json
{
  "turn_id": "turn_123",
  "session_id": "abc123",
  "status": "ready",
  "stage": "done",
  "display_text": "The kitchen light is on.",
  "transcript": "turn on the kitchen light",
  "audio_ready": true,
  "audio_url": "/api/device/turns/turn_123/audio",
  "audio_mime": "audio/wav",
  "audio_format": "wav",
  "audio_bytes": 2918400,
  "audio_rate": 24000,
  "audio_channels": 1,
  "audio_bits": 16
}
```

A turn belongs to the device that created it; another `X-Device-Id` gets `404`.

### `GET /api/device/turns/{turn_id}/audio`

Serves the spoken reply with `Content-Type: audio/wav`. Supports `HEAD` and
`Range` requests, so a device can stream or resume the download. Format headers
are repeated as `X-Audio-Format`, `X-Audio-Rate`, `X-Audio-Channels` and
`X-Audio-Bits`.

`?format=pcm` strips the WAV header and serves `audio/pcm` for devices that
decode raw samples instead of parsing RIFF.

### `POST /api/device/turns/{turn_id}/cancel`

Cancels the turn. The status becomes `canceled` once the agent stops.

### `POST /api/device/sessions`

Starts a new conversation for the device and relinks it. Returns `201` with
`{"session_id": "..."}`. The old session stays available in the web UI.

## What device turns do differently

- **No interactive tools.** `ask_clarification`, `present_plan` and `send_voice`
  are removed from the tool list and rejected at execution. A question is simply
  part of the spoken reply; the user answers with the next audio upload.
- **No approvals.** The risk gate stays active with a deny-all handler, so a
  risky tool fails with "Execution denied by user." instead of waiting for a
  button nobody can press. Safe tools keep working.
- **Deterministic speech.** Every reply is spoken once from the final answer, not
  through `send_voice`, so there is exactly one audio file per turn.
- **Capped speech.** `DEVICE_REPLY_MAX_CHARS` limits what is spoken; the full
  answer and the transcript stay in the session and in the web UI.

## ESP32 notes

- 8 MB of PSRAM is enough for a typical reply: 900 characters at 24 kHz, 16-bit
  mono is about 2.9 MB. `Range` support makes partial playback possible without
  buffering the whole file.
- Upload WAV PCM16 mono 16 kHz (about 480 KB per minute) to keep the request
  small; raw PCM is accepted with `Content-Type: audio/pcm`.
- The screen can show `stage` while polling and `display_text` when
  `status=ready`; `display_text` is plain text, capped at about 180 characters.
- A cold speech model can take several seconds to load on the first turn. Poll
  patiently; `DEVICE_TURN_TIMEOUT_MINUTES` is the hard limit.

## Firmware flow

```text
boot      → GET  /api/device/health            (show "ready" or the error)
button    → record locally
release   → POST /api/device/messages          (raw WAV body)
          → poll GET /api/device/turns/{id}    (show stage)
ready     → GET  /api/device/turns/{id}/audio  (play)
            show display_text
button    → POST /api/device/messages with X-Device-Interrupt: 1 to barge in
long press→ POST /api/device/sessions          (new conversation)
```
