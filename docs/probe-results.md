# Pruebas de Ollama

Registro de pruebas manuales ejecutadas contra Ollama local.

## Entorno

- Base URL: `http://localhost:11434`
- Ollama version: `0.24.0`
- Fecha de prueba inicial: `2026-05-16`
- Sistema: Windows, Go `1.24.1`

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

## Pruebas Pendientes

- Ejecutar probes contra `192.168.0.121:11434` con `--base-url` o `OLLAMA_BASE_URL`.
- Probar mas modelos con tools para detectar diferencias de formato.
- Probar modelos vision con imagenes reales de distintos formatos: JPG, PNG valido, WebP si Ollama lo acepta.
- Confirmar audio cuando exista flujo REST documentado o verificable.
- Registrar resultados automaticamente desde la CLI para no depender de copiar salidas manuales.
