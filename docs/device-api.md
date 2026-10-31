# Device Channel API

The device channel lets a small audio device — an ESP32 with a microphone, a
speaker and a screen — talk to ollamabot audio-to-audio. The device records a
clip, uploads it, polls for progress and downloads a spoken reply.

A device turn is a normal session turn: it runs `engine.ProcessTurn` with its
own conversation, appears in the web UI with its history and its spoken reply,
and uses the same tools, memory and workspace as every other channel. The only
difference is the medium: input and output are audio.

Every endpoint is under `/api/device/` and is mounted by the web server, so the
device channel needs `SERVER_ENABLED=true`.

## Requirements

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

`DEVICE_ENABLED=false` is the default and makes every device route answer `404`.

## Quick start

The loop is: upload a clip, poll the turn, download the reply.

```bash
HOST=http://192.168.1.10:8080
TOKEN=change-me
DEV=kitchen-1

# 1. boot check
curl -s "$HOST/api/device/health" -H "X-Device-Id: $DEV" -H "X-Device-Token: $TOKEN"

# 2. upload a WAV clip (raw bytes, not multipart)
curl -s -X POST "$HOST/api/device/messages" \
  -H "X-Device-Id: $DEV" -H "X-Device-Token: $TOKEN" \
  -H "Content-Type: audio/wav" \
  --data-binary @clip.wav
# {"turn_id":"9f2c...","session_id":"4b1d...","status":"queued"}

# 3. poll until status is ready, failed or canceled
curl -s "$HOST/api/device/turns/9f2c..." -H "X-Device-Id: $DEV" -H "X-Device-Token: $TOKEN"

# 4. download the spoken reply
curl -s -o reply.wav "$HOST/api/device/turns/9f2c.../audio" \
  -H "X-Device-Id: $DEV" -H "X-Device-Token: $TOKEN"
```

## Authentication

Every request carries the shared token in `X-Device-Token` or
`Authorization: Bearer <token>`. The token is compared in constant time. Device
routes are exempt from the web password: the device never needs it, and the web
UI password does not apply to this namespace.

Requests that identify a conversation also send `X-Device-Id`
(`[A-Za-z0-9_-]{1,64}`). The ID maps the physical device to its session in
`sessions/devices.json`; the session survives restarts and is visible in the web
UI as `Device <id>`. Two devices using the same ID share one conversation, so
derive it from hardware (MAC, chip ID) and keep it stable.

A disabled channel answers `404` so it is not advertised. A bad or missing token
answers `401`.

## Audio formats

**Upload.** The body is the raw audio file, never multipart. The server does not
transcode or wrap it: the bytes are base64-encoded and sent to the transcription
model as one inline attachment. `Content-Type` is validated but only recorded, so
it cannot repair a headerless payload.

Send **WAV, RIFF, PCM 16-bit, mono, 16 kHz**. That is self-describing, small
(~480 KB per minute) and works with every transcription model. `audio/pcm`,
`audio/ogg`, `audio/mpeg` and `audio/mp4` are accepted and forwarded untouched;
whether they transcribe depends on the model decoding that container, and raw
PCM with no header will usually come back as garbage. An absent `Content-Type`
is treated as WAV.

**Reply.** The spoken answer is served as WAV (`audio/wav`), with
`X-Audio-Format`, `X-Audio-Rate`, `X-Audio-Channels` and `X-Audio-Bits`
describing the actual stream. `?format=pcm` strips the RIFF header and serves
`audio/pcm` for a decoder that wants raw samples; the header values still apply.
The reply format is whatever the speech model produces, so read `audio_rate`,
`audio_channels` and `audio_bits` from the turn instead of assuming 24 kHz.

## Turn lifecycle

```text
queued → processing → speaking → ready
                    ↘ failed
                    ↘ canceled
```

| Status | `stage` | What the device should do |
|--------|---------|---------------------------|
| `queued` | `queued` | Show "sending"; the turn has not started yet |
| `processing` | `thinking` | Show a thinking animation |
| `processing` | `heard you` | Show the transcript; the agent is working |
| `speaking` | `speaking` | Audio is being synthesized; show the final text if you have it |
| `ready` | `done` | Show `display_text` and play `audio_url` when `audio_ready` |
| `failed` | `failed` | Show `error` briefly |
| `canceled` | `canceled` | Return to idle silently |

`display_text` is plain text for a small screen: markdown, code blocks, links
and table pipes are stripped and the result is capped at about 180 characters
(`"No reply."` when the answer is empty). `transcript` appears once the upload
has been transcribed and holds what the device said, for confirmation or
debugging.

If speech synthesis fails the turn still becomes `ready` with
`audio_ready: false`: the screen keeps the text, `error` stays empty and the
server logs the reason.

## Endpoints

### `GET /api/device/health`

Cheap boot check. Always `200`, even when the channel is not ready.

```json
{
  "ok": true,
  "can_transcribe": true,
  "can_speak": true,
  "transcription_model": "qwen-audio",
  "speech_model": "phonon",
  "device_id": "kitchen-1",
  "session_id": "abc123"
}
```

| Field | Present when | Meaning |
|-------|--------------|---------|
| `ok` | always | Both audio directions are ready |
| `error` | `ok` is false | Why the channel cannot serve yet |
| `can_transcribe` / `can_speak` | always | Per-direction capability |
| `transcription_model` / `speech_model` | always | Resolved role models, may be empty |
| `device_id` | `X-Device-Id` was sent | Echo of the header |
| `session_id` | the device already has a conversation | Current session |

### `POST /api/device/messages`

Starts a turn. The body is the audio bytes.

| Header | Required | Meaning |
|--------|----------|---------|
| `X-Device-Id` | yes | Device identity, `[A-Za-z0-9_-]{1,64}` |
| `X-Device-Token` | yes | Shared secret |
| `Content-Type` | no | Audio type; absent means `audio/wav` |
| `X-Device-Interrupt` | no | `1` cancels the turn in flight and starts this one |

Response `202`:

```json
{ "turn_id": "turn_123", "session_id": "abc123", "status": "queued" }
```

When a turn is already running for that device and `X-Device-Interrupt` is not
`1`, the response is `409` with the **current turn JSON** (same shape as
`GET /api/device/turns/{id}`) so the device can keep polling instead of losing
track of the running turn.

### `GET /api/device/turns/{turn_id}`

Poll every ~300–500 ms while the device waits, and back off to ~1 s after the
first ten seconds. The full payload:

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
  "audio_bits": 16,
  "created_at": "2026-10-31T18:50:14Z",
  "updated_at": "2026-10-31T18:50:31Z"
}
```

| Field | Present when | Meaning |
|-------|--------------|---------|
| `turn_id`, `session_id`, `status` | always | Identity and state |
| `stage` | not `queued` failures | Short screen label from the table above |
| `display_text` | `ready` | Plain text for the screen, ~180 chars |
| `transcript` | transcription succeeded | What the device said, as text |
| `audio_ready` | always | `true` only when a reply file exists |
| `audio_url` | `audio_ready` | Relative path to download the reply |
| `audio_mime`, `audio_format` | `audio_ready` | Container of the reply |
| `audio_bytes`, `audio_rate`, `audio_channels`, `audio_bits` | `audio_ready` | Size and stream layout |
| `error` | `failed` / `canceled` | Human-readable reason |
| `created_at`, `updated_at` | always | RFC 3339 timestamp in server local time |

A turn belongs to the device that created it; another `X-Device-Id` gets `404`,
so a device cannot read someone else's reply.

### `GET /api/device/turns/{turn_id}/audio`

Serves the spoken reply. Supports `HEAD` and `Range` requests, so a device can
stream, resume or seek without buffering the whole file; `Content-Length` and
`Accept-Ranges` come from `http.ServeContent`. Responses carry
`Cache-Control: private, max-age=3600` and `X-Device-Session`.

`?format=pcm` serves the same samples without the RIFF header as `audio/pcm`.
Only the reply is affected: uploads are never converted.

While the reply has no audio (`audio_ready: false`) this endpoint answers `409`,
and an unknown or foreign turn answers `404`, so poll the turn first.

### `POST /api/device/turns/{turn_id}/cancel`

Cancels the turn and returns its JSON immediately. `status` becomes `canceled`
once the agent goroutine stops; poll the turn if the device needs to confirm.
Cancelling an already finished turn is harmless.

### `POST /api/device/sessions`

Starts a new conversation for the device and relinks it. Returns `201` with
`{"session_id": "..."}`. The previous session stays available in the web UI, so
nothing is lost. The call cancels and waits for the running turn (up to 10
seconds); if the turn refuses to stop it answers `409`.

## Errors

All error bodies are `{"error": "<message>"}`.

| Status | When |
|--------|------|
| `400` | Invalid or missing `X-Device-Id`, unreadable body, or payload under 128 bytes |
| `401` | Missing or wrong token |
| `404` | Channel disabled, unknown turn, or a turn owned by another device |
| `409` | A turn is already running for this device and the upload did not interrupt, the previous turn could not be stopped in time, or reply audio was requested before it exists |
| `413` | Body larger than `DEVICE_MAX_AUDIO_MB` |
| `415` | `Content-Type` is not an accepted audio type |
| `500` | The device session could not be created or read |
| `503` | `OLLAMA_MODEL_SPEECH` is empty, so no audio reply is possible |

## Limits and timing

| Concern | Value |
|---------|-------|
| Largest upload | `DEVICE_MAX_AUDIO_MB` (15 MiB) |
| Smallest upload | 128 bytes, to reject empty recordings |
| Active turns per device | 1 |
| Turn budget | `DEVICE_TURN_TIMEOUT_MINUTES` (5 min), then `failed` |
| Speech synthesis budget | 3 min of its own, after the agent finishes |
| Turn records in memory | 2 hours after the last update, then pruned |
| Spoken reply length | `DEVICE_REPLY_MAX_CHARS` (900 chars ≈ 60 s of audio) |

Concurrent requests from the same device are serialized: uploads answer `409`
instead of starting a second agent turn, and `X-Device-Interrupt: 1` waits up to
10 seconds for the previous turn to leave before starting the new one. Two
devices with different IDs run independently.

A cold model can take several seconds to load on the first turn. Poll patiently;
the turn budget is the hard limit.

## Restart behavior

Turn records live in memory: after a restart, polling an old `turn_id` answers
`404`, and an in-flight turn is lost — the device speaks again to recover. The
conversation itself is unaffected: the session, its messages, the uploaded audio
and the spoken replies are all stored on disk, and the `device_id → session_id`
link persists in `sessions/devices.json`. The web UI shows device turns like any
other session.

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
- Record WAV PCM16 mono 16 kHz: about 480 KB per minute upstream and the format
  every transcription model understands.
- The screen can show `stage` while polling and `display_text` when
  `status=ready`.
- Keep one TCP connection per poll and disable keep-alive if memory is tight;
  the payloads are tiny but the connection churn is not.

## Firmware flow

```text
boot      → GET  /api/device/health            (show "ready" or the error)
button    → record locally
release   → POST /api/device/messages          (raw WAV body)
          → poll GET /api/device/turns/{id}    (show stage, then transcript)
ready     → GET  /api/device/turns/{id}/audio  (play)
            show display_text
button    → POST /api/device/messages with X-Device-Interrupt: 1 to barge in
long press→ POST /api/device/sessions          (new conversation)
```
