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
- `.env.example` con claves iniciales.
- Tests unitarios para config, cliente Ollama, capacidades y generacion de docs.
- Documentacion local generada en `docs/ollama-reference.md`.
- Inventario local generado en `docs/local-model-inventory.md`.

Comandos disponibles:

```powershell
go run ./cmd/ollamabot probe models
go run ./cmd/ollamabot probe chat --model qwen3:8b
go run ./cmd/ollamabot probe tools --model qwen3:8b
go run ./cmd/ollamabot probe json --model qwen3:8b
go run ./cmd/ollamabot probe vision --model qwen3-vl:4b --image C:\path\image.jpg
go run ./cmd/ollamabot probe thinking --model qwen3:8b
go run ./cmd/ollamabot probe embeddings --model nomic-embed-text:latest
go run ./cmd/ollamabot probe audio --model test-gemma4-vision:latest
go run ./cmd/ollamabot docs generate --out docs
```

## Decisiones Tomadas

- Stack: Go.
- Primera fase: documentacion + probes, antes de Telegram/web/agente completo.
- Sin dependencias externas por ahora; parser `.env` propio y CLI basada en `flag`.
- Las capacidades reportadas por `/api/show.capabilities` se consideran `comprobado`.
- Los encoders vistos en `projector_info`, como audio o vision, se consideran `inferido` si no hay prueba end-to-end.
- Audio queda experimental hasta confirmar un payload REST estable.
- Video queda pendiente; la estrategia inicial sera extraer frames y enviarlos a modelos de vision.

## Pendiente

- Agregar persistencia de resultados de probes en JSON/Markdown sin depender de copiar salida de consola.
- Crear canal web local para enviar prompts, imagenes y ver metadata de modelos.
- Crear canal Telegram.
- Crear router de modelos por capacidad: texto, tools, vision, embeddings, thinking.
- Definir interfaz de tools internas del agente.
- Implementar memoria/conversaciones por canal.
- Definir seguridad para tools: allowlist, timeouts, auditoria y confirmacion de acciones riesgosas.
- Confirmar audio con pruebas reales cuando Ollama exponga o documente un payload estable.
- Definir soporte de video como pipeline de frames.

## Riesgos y Notas

- Algunos modelos reportan capacidades que pueden fallar en prompts reales; por eso los probes end-to-end son necesarios.
- El probe de chat con `qwen3:8b` respondio `ok /think`; el transporte funciona, pero hay que limpiar tokens de control si aparecen en respuestas finales.
- El acceso a `192.168.0.121:11434` no se valido desde el sandbox en la primera exploracion; queda cubierto por `OLLAMA_BASE_URL`, pero requiere prueba en entorno normal.
