# Progreso del Proyecto

## Objetivo General

Crear un agente autonomo modular basado en modelos locales de Ollama. El agente debe detectar que modelos hay instalados, que capacidades tienen, y usar esas capacidades segun corresponda. Los canales previstos son Telegram y una web propia para pruebas rapidas.

## Fase 1 Implementada

Se implemento una base Go enfocada en documentacion y probes verificables.

Hecho:

- Proyecto Go creado con modulo `github.com/jonathanhecl/ollamabot`.
- CLI principal en `cmd/ollamabot`.
- Configuracion desde `.env` y variables de entorno.
- Cliente HTTP nativo para Ollama en `internal/ollama`.
- Mapeo de capacidades en `internal/capabilities`.
- Probes en `internal/probe`.
- Generador Markdown en `internal/docs`.
- Cache JSON de resultados esperados en `internal/cache`.
- Servidor web local en `internal/web`.
- `.env.example` con claves iniciales.
- Ejecucion normal sin parametros: carga `.env`, lo crea interactivamente si falta, y levanta la web si `WEB_ENABLED=true`.
- Configuracion de URL de Ollama desde la web, persistida en `.env`.
- Modal de modelos en la web en lugar de tabla permanente.
- Chat web con streaming SSE desde Ollama.
- Visualizacion incremental de `thinking` en bloque separado.
- Adjuntos multimodales desde archivo o paste: imagenes y audio usan el payload multimodal de Ollama.
- Preparacion visual para tool calls: si el modelo devuelve `tool_calls`, la web muestra nombre y parametros.
- Tests unitarios para config, cliente Ollama, capacidades y generacion de docs.
- Documentacion local generada en `docs/ollama-reference.md`.
- Inventario local generado en `docs/local-model-inventory.md`.
- Snapshot cacheado generado en `docs/probe-cache.json`.
- Web validada en `http://localhost:8080`.

Comandos disponibles:

```powershell
go run ./cmd/ollamabot probe models
go run ./cmd/ollamabot probe snapshot --out docs/probe-cache.json
go run ./cmd/ollamabot probe chat --model qwen3:8b
go run ./cmd/ollamabot probe tools --model qwen3:8b
go run ./cmd/ollamabot probe json --model qwen3:8b
go run ./cmd/ollamabot probe vision --model qwen3-vl:4b --image C:\path\image.jpg
go run ./cmd/ollamabot probe thinking --model qwen3:8b
go run ./cmd/ollamabot probe embeddings --model nomic-embed-text:latest
go run ./cmd/ollamabot probe audio --model test-gemma4-vision:latest
go run ./cmd/ollamabot docs generate --out docs
go run ./cmd/ollamabot serve --addr :8080 --cache docs/probe-cache.json
```

## Decisiones Tomadas

- Stack: Go.
- Primera fase: documentacion + probes, antes de Telegram/web/agente completo.
- Sin dependencias externas por ahora; parser `.env` propio y CLI basada en `flag`.
- Las capacidades reportadas por `/api/show.capabilities` se consideran `comprobado`.
- El consumo de memoria en vivo sale de `GET /api/ps`, especificamente `size_vram` para modelos cargados.
- Los encoders vistos en `projector_info`, como audio o vision, se consideran `inferido` si no hay prueba end-to-end.
- Audio queda experimental hasta confirmar un payload REST estable.
- `gemma4:e2b` reporta `audio` en `/api/show.capabilities`, pero la prueba local end-to-end con WAV hizo caer el runner de Ollama con error 500.
- Video queda pendiente; la estrategia inicial sera extraer frames y enviarlos a modelos de vision.

## Pendiente

- Mejorar persistencia de probes para registrar tambien ejecuciones individuales de chat/tools/json/vision.
- Agregar ejecucion real de tools internas y retorno automatico al modelo.
- Agregar historial/memoria por conversacion y canal.
- Agregar drag and drop de archivos a la web.
- Crear canal Telegram.
- Crear router de modelos por capacidad: texto, tools, vision, embeddings, thinking.
- Definir interfaz de tools internas del agente.
- Implementar memoria/conversaciones por canal.
- Definir seguridad para tools: allowlist, timeouts, auditoria y confirmacion de acciones riesgosas.
- Confirmar audio con pruebas reales cuando Ollama exponga o documente un payload estable.
- Reprobar audio de `gemma4:e2b` cuando Ollama actualice el runner o haya logs que indiquen el motivo del crash.
- Definir soporte de video como pipeline de frames.

## Riesgos y Notas

- Algunos modelos reportan capacidades que pueden fallar en prompts reales; por eso los probes end-to-end son necesarios.
- El probe de chat con `qwen3:8b` respondio `ok /think`; el transporte funciona, pero hay que limpiar tokens de control si aparecen en respuestas finales.
- El acceso a `192.168.0.121:11434` no se valido desde el sandbox en la primera exploracion; queda cubierto por `OLLAMA_BASE_URL`, pero requiere prueba en entorno normal.
- La web usa datos vivos cuando Ollama responde y puede caer al snapshot cacheado si el inventario vivo falla.
