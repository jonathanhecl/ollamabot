# Ollama API Reference

Generated: 2026-05-16T23:27:50-03:00

Base URL: `http://localhost:11434`

Ollama version: `0.24.0`

## Sources

- [Ollama API introduction](https://docs.ollama.com/api/introduction)
- [Chat API](https://docs.ollama.com/api/chat)
- [List models](https://docs.ollama.com/api/tags)
- [Vision](https://docs.ollama.com/capabilities/vision)
- [Tool calling](https://docs.ollama.com/capabilities/tool-calling)
- [Structured outputs](https://docs.ollama.com/capabilities/structured-outputs)
- [Thinking](https://docs.ollama.com/capabilities/thinking)
- [Embeddings](https://docs.ollama.com/capabilities/embeddings)
- [OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [Gemma4 model page](https://ollama.com/library/gemma4%3Alatest)
- [Audio input feature request](https://github.com/ollama/ollama/issues/11798)

## Confirmed REST Patterns

- Text chat: `POST /api/chat` with `messages` and `stream:false`.
- Vision: `POST /api/chat` with `messages[].images` containing raw base64 image data.
- Tool calling: send `tools`, read `message.tool_calls`, execute locally, append `role:"tool"` with `tool_name`, then call chat again.
- Structured output: send `format:"json"` or a JSON Schema object in `format`, then validate the returned content.
- Thinking: send `think:true` or a model-specific level when required; read `message.thinking` separately from `message.content`.
- Embeddings: `POST /api/embed` with text input; response contains `embeddings`.

## Minimal Payloads

### Text

```json
{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "Say ok"}],
  "stream": false
}
```

### Image

`images` values must be raw base64, not a `data:image/...` URI.

```json
{
  "model": "qwen3-vl:4b",
  "messages": [{
    "role": "user",
    "content": "Describe this image.",
    "images": ["<raw-base64-image>"]
  }],
  "stream": false
}
```

### Tool Calling

```json
{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "What is the temperature in Tokyo?"}],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_temperature",
      "description": "Get the current temperature for a city",
      "parameters": {
        "type": "object",
        "required": ["city"],
        "properties": {"city": {"type": "string"}}
      }
    }
  }],
  "stream": false
}
```

### Structured JSON

```json
{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "Return a JSON object named ollamabot."}],
  "format": {
    "type": "object",
    "properties": {"name": {"type": "string"}, "ok": {"type": "boolean"}},
    "required": ["name", "ok"]
  },
  "stream": false
}
```

### Thinking

```json
{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "How many r letters are in strawberry?"}],
  "think": true,
  "stream": false
}
```

### Embeddings

```json
{
  "model": "nomic-embed-text:latest",
  "input": "The quick brown fox jumps over the lazy dog."
}
```

## Pending Confirmation

- Audio: some models expose `projector_info.clip.has_audio_encoder`, but this project does not mark audio as confirmed until a stable REST payload is verified locally.
- Video: no native Ollama REST video input is treated as confirmed; future support should start as frame extraction into vision models.
