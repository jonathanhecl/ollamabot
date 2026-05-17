# Pruebas de Ollama

Registro de pruebas manuales ejecutadas contra Ollama local.

## Entorno

- Base URL: `http://localhost:11434`
- Ollama version: `0.24.0`
- Fecha de prueba inicial: `2026-05-16`
- Ultima actualizacion: `2026-05-17`
- Sistema: Windows, Go `1.24.1`

## Ejecucion Normal sin Parametros

Comando probado con `.env` temporal inexistente:

```powershell
go run ./cmd/ollamabot -env C:\tmp\ollamabot-test.env
```

Entradas:

```text
http://localhost:11434
n
9090
```

Resultado:

```text
No encontre C:\tmp\ollamabot-test.env. Vamos a crearlo con la configuracion basica.
Ollama URL [http://localhost:11434]:
Levantar servidor web? (s/n) [s]:
Puerto web [8080]:
Listo, guarde C:\tmp\ollamabot-test.env.
Servidor web desactivado en .env (WEB_ENABLED=false).
```

Estado: comprobado.

## Suite Go

Comando:

```powershell
go test ./...
```

Resultado:

```text
ok github.com/jonathanhecl/ollamabot/internal/capabilities
ok github.com/jonathanhecl/ollamabot/internal/config
ok github.com/jonathanhecl/ollamabot/internal/docs
ok github.com/jonathanhecl/ollamabot/internal/ollama
```

Tambien se validaron:

```powershell
go vet ./...
go build ./cmd/ollamabot
```

## Snapshot Cacheado

Comando:

```powershell
go run ./cmd/ollamabot probe snapshot --out docs/probe-cache.json
```

Resultado:

```text
wrote docs/probe-cache.json
```

Estado: comprobado. Este archivo guarda inventario, modelos cargados y resultados esperados base para no repetir todos los probes en cada arranque.

## Probes Manuales

### Modelos

Comando:

```powershell
go run ./cmd/ollamabot probe models
```

Resultado: comprobado. Se listaron modelos locales y capacidades normalizadas. El inventario completo esta en [local-model-inventory.md](local-model-inventory.md).

### Chat

Comando:

```powershell
go run ./cmd/ollamabot probe chat --model qwen3:8b
```

Resultado:

```text
chat qwen3:8b comprobado ok /think
```

Nota: el transporte funciona; hay que contemplar limpieza de tokens de control como `/think` en respuestas finales si aparecen.

### Tools

Comando:

```powershell
go run ./cmd/ollamabot probe tools --model qwen3:8b
```

Resultado:

```text
tools qwen3:8b comprobado The current temperature in Tokyo is 18°C.
```

Se comprobo el ciclo completo: definicion de tool, `message.tool_calls`, ejecucion local simulada y segundo request con respuesta final.

### JSON Estructurado

Comando:

```powershell
go run ./cmd/ollamabot probe json --model qwen3:8b
```

Resultado:

```json
{
  "name": "ollamabot",
  "ok": true
}
```

Estado: comprobado.

### Thinking

Comando:

```powershell
go run ./cmd/ollamabot probe thinking --model qwen3:8b
```

Resultado:

```text
thinking qwen3:8b comprobado message.thinking returned
```

Estado: comprobado.

### Embeddings

Comando:

```powershell
go run ./cmd/ollamabot probe embeddings --model nomic-embed-text:latest
```

Resultado:

```text
embeddings nomic-embed-text:latest comprobado vector length 768
```

Estado: comprobado.

### Vision

Primer intento: PNG base64 minimo temporal. Fallo porque Ollama lo rechazo como PNG invalido.

Resultado:

```text
png: invalid format: invalid checksum
```

Segundo intento: JPEG real generado temporalmente en `C:\tmp`.

Comando:

```powershell
go run ./cmd/ollamabot probe vision --model qwen3-vl:4b --image C:\tmp\ollamabot-probe.jpg
```

Resultado:

```text
vision qwen3-vl:4b comprobado The image is a solid, uniform red color with no additional elements or variations.
```

Estado: comprobado.

### Audio

Comando:

```powershell
go run ./cmd/ollamabot probe audio --model test-gemma4-vision:latest
```

Resultado:

```text
audio test-gemma4-vision:latest inferido audio encoder detected in projector_info; REST payload remains unconfirmed
```

Estado: inferido. No se marca como comprobado hasta probar un payload REST estable.

### Audio con gemma4:e2b

`gemma4:e2b` fue instalado localmente y `/api/show` reporto estas capacidades:

```text
completion, vision, audio, tools, thinking
```

Prueba end-to-end con WAV temporal de 1 segundo:

```powershell
go run ./cmd/ollamabot probe audio --model gemma4:e2b --audio C:\tmp\ollamabot-tone.wav
```

Resultado en dos intentos:

```text
audio gemma4:e2b pendiente ollama POST /api/chat failed: status 500:
{"error":"model runner has unexpectedly stopped, this may be due to resource limitations or an internal error, check ollama server logs for details"}
```

Estado: pendiente para uso real. Metadata confirma audio, pero el runner local falla al procesar el archivo.

### Web Local

Comando:

```powershell
go run ./cmd/ollamabot serve --addr :8080 --cache docs/probe-cache.json
```

Health:

```powershell
Invoke-RestMethod -Uri http://localhost:8080/api/health
```

Resultado:

```json
{
  "ollama_version": "0.24.0",
  "status": "ok"
}
```

Modelos:

```powershell
Invoke-RestMethod -Uri http://localhost:8080/api/models
```

Resultado resumido:

```json
{
  "base_url": "http://localhost:11434",
  "ollama_version": "0.24.0",
  "from_cache": false,
  "models": 21
}
```

Chat:

```powershell
POST http://localhost:8080/api/chat
```

Payload:

```json
{
  "model": "qwen3:8b",
  "messages": [
    {"role": "user", "content": "Reply with only: pong"}
  ],
  "think": false
}
```

Resultado:

```json
{
  "message": {
    "role": "assistant",
    "content": "pong"
  },
  "model": "qwen3:8b"
}
```

Estado: comprobado.

## Pruebas Pendientes

- Ejecutar probes contra `192.168.0.121:11434` con `--base-url` o `OLLAMA_BASE_URL`.
- Probar mas modelos con tools para detectar diferencias de formato.
- Probar modelos vision con imagenes reales de distintos formatos: JPG, PNG valido, WebP si Ollama lo acepta.
- Confirmar audio cuando exista flujo REST documentado o verificable.
- Registrar resultados automaticamente desde la CLI para no depender de copiar salidas manuales.
- Agregar pruebas visuales de la web con navegador.
