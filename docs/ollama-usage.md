# Ollama Usage in OllamaBot

This guide outlines how Ollama is utilized within this project. The base URL is loaded from `.env`:

```env
OLLAMA_BASE_URL=http://localhost:11434
WEB_ENABLED=true
WEB_ADDR=:8080
```

Standard execution:

```powershell
go run ./cmd/ollamabot
```

If the `.env` file does not exist, the program prompts via terminal for:
- Ollama base URL.
- Whether to start the web server.
- Web port.

The CLI also supports overrides:

```powershell
go run ./cmd/ollamabot --base-url http://localhost:11434 probe models
```

## Used Endpoints

### Version

```http
GET /api/version
```

Returns the Ollama version. Used to document which runtime generated the results.

### Installed Models

```http
GET /api/tags
```

Returns the list of local models, including name, size, digest, and basic details.

### Model Metadata

```http
POST /api/show
```

Payload:

```json
{
  "model": "qwen3:8b"
}
```

Key fields:
- `capabilities`: Primary source for `completion`, `tools`, `thinking`, `vision`, and `embedding`.
- `model_info`: Retrieves active context length, architecture, and other technical metadata.
- `projector_info`: Can indicate multimodal encoders such as `clip.has_audio_encoder` or `clip.has_vision_encoder`.

### Loaded Models and Memory

```http
GET /api/ps
```

Returns models currently loaded in memory. Fields used by the Web UI:
- `size`: Total size of the model.
- `size_vram`: VRAM/RAM memory used by the loaded model.
- `expires_at`: Timestamp when Ollama will unload the model if it remains inactive.
- `context_length`: Active context length for the loaded model instance.

If a model appears in `/api/tags` but not in `/api/ps`, the web dashboard displays it as available but not loaded.

## Text Chat

```json
{
  "model": "qwen3:8b",
  "messages": [
    {"role": "user", "content": "Say ok"}
  ],
  "stream": false
}
```

Endpoint:

```http
POST /api/chat
```

Expected response: `message.content`.

## Images

Ollama receives images as raw base64 strings in `messages[].images`. Do not prepend the `data:image/...` URI scheme.

```json
{
  "model": "qwen3-vl:4b",
  "messages": [
    {
      "role": "user",
      "content": "Describe this image.",
      "images": ["<raw-base64-image>"]
    }
  ],
  "stream": false
}
```

Probe:

```powershell
go run ./cmd/ollamabot probe vision --model qwen3-vl:4b --image C:\path\image.jpg
```

## Tools

A list of functions is sent under the `tools` array. If the model decides to invoke a tool, it returns `message.tool_calls`.

First request:

```json
{
  "model": "qwen3:8b",
  "messages": [
    {"role": "user", "content": "What is the temperature in Tokyo?"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_temperature",
        "description": "Get the current temperature for a city",
        "parameters": {
          "type": "object",
          "required": ["city"],
          "properties": {
            "city": {"type": "string"}
          }
        }
      }
    }
  ],
  "stream": false
}
```

Then, the program executes the tool locally and appends the tool message:

```json
{
  "role": "tool",
  "tool_name": "get_temperature",
  "content": "18C"
}
```

Subsequently, a second `POST /api/chat` is performed for the model to synthesize the final response.

## Structured JSON

Ollama accepts `format` as a JSON Schema object.

```json
{
  "model": "qwen3:8b",
  "messages": [
    {"role": "user", "content": "Return JSON for a probe named ollamabot with ok true."}
  ],
  "format": {
    "type": "object",
    "properties": {
      "name": {"type": "string"},
      "ok": {"type": "boolean"}
    },
    "required": ["name", "ok"]
  },
  "stream": false
}
```

The project validates that `message.content` is parseable JSON.

## Thinking

Activated via `think:true`.

```json
{
  "model": "qwen3:8b",
  "messages": [
    {"role": "user", "content": "How many r letters are in strawberry?"}
  ],
  "think": true,
  "stream": false
}
```

If Ollama returns `message.thinking`, it is marked as confirmed. If it does not appear but `/api/show` reports `thinking`, it is marked as confirmed via model metadata.

## Embeddings

```http
POST /api/embed
```

Payload:

```json
{
  "model": "nomic-embed-text:latest",
  "input": "The quick brown fox jumps over the lazy dog."
}
```

Considered valid if `embeddings[0]` exists and has a length greater than zero.

## Audio

Some models expose audio capability directly under `/api/show.capabilities` (e.g., `gemma4:e2b` reports `audio`). Other models might only expose encoder metadata:

```text
projector_info.clip.has_audio_encoder = true
```

If `audio` appears in `capabilities`, the model inventory marks it as confirmed at the metadata level. To validate end-to-end:

```powershell
go run ./cmd/ollamabot probe audio --model gemma4:e2b --audio C:\path\audio.wav
```

Local tests performed with a short WAV file sent the audio as raw base64 under `messages[].images`, matching patterns observed in issues for Ollama/Gemma 4. On this machine, the Ollama runner returned a 500 error and stopped, so real audio usage is marked as pending verification, even if the capability is reported via metadata.

## Local Web server

Command:

```powershell
go run ./cmd/ollamabot serve --addr :8080 --cache docs/probe-cache.json
```

The server exposes:
- `GET /api/health`: Verifies connectivity with Ollama.
- `GET /api/settings`: Reads runtime configuration.
- `POST /api/settings`: Updates Ollama URL and persists `.env`.
- `GET /api/models`: Lists available models, capabilities, loaded memory, and cache status.
- `POST /api/chat/stream`: Sends messages to the selected model and streams Server-Sent Events.

The interface allows selecting a model as `Main` from a modal and conversing with it. Based on capabilities:
- `thinking`: Enables the `think` toggle and renders the thinking block.
- `vision`: Enables attaching/pasting images.
- `audio`: Enables attaching/pasting audio.
- If a capability is not available in the active model, the UI hides the control and discards that attachment type.
- Attachments show a preview before sending and remain visible in the chat timeline (images expand on click, and audios play via native browser controls).
- `tools`: If Ollama returns `tool_calls`, name and parameters are rendered; the actual execution of tools is deferred to the agent layer.

Current SSE Events:

```text
event: thinking
data: "..."

event: content
data: "..."

event: tool_call
data: {"function": {"name": "...", "arguments": {...}}}

event: done
data: {"model": "...", "reason": "..."}
```
