# Uso de Ollama en OllamaBot

Esta guia resume como se usa Ollama desde este proyecto. La URL base sale de `.env`:

```env
OLLAMA_BASE_URL=http://localhost:11434
```

La CLI tambien permite override:

```powershell
go run ./cmd/ollamabot --base-url http://localhost:11434 probe models
```

## Endpoints Usados

### Version

```http
GET /api/version
```

Devuelve la version de Ollama. Se usa para documentar contra que runtime se generaron los resultados.

### Modelos Instalados

```http
GET /api/tags
```

Devuelve la lista de modelos locales, con nombre, tamanio, digest y detalles basicos.

### Metadata de Modelo

```http
POST /api/show
```

Payload:

```json
{
  "model": "qwen3:8b"
}
```

Campos importantes:

- `capabilities`: fuente primaria para `completion`, `tools`, `thinking`, `vision`, `embedding`.
- `model_info`: permite obtener contexto, arquitectura y metadata tecnica.
- `projector_info`: puede indicar encoders multimodales como `clip.has_audio_encoder` o `clip.has_vision_encoder`.

## Chat de Texto

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

Respuesta esperada: `message.content`.

## Imagenes

Ollama recibe imagenes como base64 crudo en `messages[].images`. No usar prefijo `data:image/...`.

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

Se envia una lista `tools` con definiciones tipo function. Si el modelo decide llamar una tool, responde `message.tool_calls`.

Primer request:

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

Luego el programa ejecuta la tool localmente y agrega un mensaje:

```json
{
  "role": "tool",
  "tool_name": "get_temperature",
  "content": "18C"
}
```

Despues se hace un segundo `POST /api/chat` para que el modelo redacte la respuesta final.

## JSON Estructurado

Ollama acepta `format` como JSON Schema.

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

El proyecto valida que `message.content` sea JSON parseable.

## Thinking

Se activa con `think:true`.

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

Si Ollama devuelve `message.thinking`, se marca como comprobado. Si no aparece pero `/api/show` reporta `thinking`, se marca como comprobado por metadata del modelo.

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

Se considera valido si `embeddings[0]` existe y tiene longitud mayor a cero.

## Audio y Video

Audio no esta marcado como funcional todavia. Algunos modelos exponen:

```text
projector_info.clip.has_audio_encoder = true
```

Eso se guarda como `inferido`, no como `comprobado`, porque falta confirmar un payload REST estable.

Video queda pendiente. La estrategia inicial sera procesar video fuera de Ollama, extraer frames relevantes y enviarlos como imagenes a un modelo con vision.
