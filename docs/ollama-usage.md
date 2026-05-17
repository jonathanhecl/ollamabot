# Uso de Ollama en OllamaBot

Esta guia resume como se usa Ollama desde este proyecto. La URL base sale de `.env`:

```env
OLLAMA_BASE_URL=http://localhost:11434
WEB_ENABLED=true
WEB_ADDR=:8080
```

Uso normal:

```powershell
go run ./cmd/ollamabot
```

Si no existe `.env`, el programa pregunta por terminal:

- URL de Ollama.
- Si se debe levantar servidor web.
- Puerto web.

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

### Modelos Cargados y Memoria

```http
GET /api/ps
```

Devuelve modelos actualmente cargados en memoria. Campos usados por la web:

- `size`: tamanio del modelo.
- `size_vram`: memoria VRAM/RAM usada por el modelo cargado.
- `expires_at`: momento en que Ollama podria descargarlo si no se usa.
- `context_length`: contexto activo para esa carga.

Si un modelo aparece en `/api/tags` pero no en `/api/ps`, la web lo muestra como disponible pero no cargado.

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

Algunos modelos exponen audio directamente en `/api/show.capabilities`, por ejemplo `gemma4:e2b` reporta `audio`. Otros modelos pueden exponer solo metadata de encoder:

```text
projector_info.clip.has_audio_encoder = true
```

Si `audio` aparece en `capabilities`, el inventario lo marca como `comprobado` a nivel metadata. Para validar end-to-end:

```powershell
go run ./cmd/ollamabot probe audio --model gemma4:e2b --audio C:\path\audio.wav
```

La prueba local realizada con un WAV corto envio el audio como base64 crudo en `messages[].images`, siguiendo ejemplos observados en issues de Ollama/Gemma 4. En esta maquina el runner de Ollama devolvio error 500 y se detuvo, por lo que el uso real de audio queda pendiente aunque la capacidad este reportada por metadata.

Video queda pendiente. La estrategia inicial sera procesar video fuera de Ollama, extraer frames relevantes y enviarlos como imagenes a un modelo con vision.

## Web Local

Comando:

```powershell
go run ./cmd/ollamabot serve --addr :8080 --cache docs/probe-cache.json
```

La web expone:

- `GET /api/health`: verifica conexion con Ollama.
- `GET /api/settings`: lee configuracion runtime.
- `POST /api/settings`: actualiza URL de Ollama y persiste `.env`.
- `GET /api/models`: lista modelos disponibles, capacidades, memoria cargada y estado de cache.
- `POST /api/chat/stream`: envia mensajes al modelo seleccionado y devuelve Server-Sent Events.

La interfaz permite seleccionar un modelo como `Main` desde un modal y conversar con el. Segun capacidades:

- `thinking`: muestra toggle `think` y renderiza el bloque de thinking.
- `vision`: habilita adjuntar/pegar imagenes.
- `audio`: habilita adjuntar/pegar audio.
- Si una capacidad no esta disponible en el modelo activo, la UI oculta el control y descarta ese tipo de adjunto.
- Los adjuntos se muestran como preview antes de enviar y quedan visibles en el chat; imagenes se abren en grande con click y audios se reproducen con controles nativos.
- `tools`: si Ollama devuelve `tool_calls`, se muestran nombre y parametros; la ejecucion real de tools queda para la capa de agente.

Eventos SSE actuales:

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
