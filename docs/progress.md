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
- Previews de imagen/audio antes de enviar y dentro del chat despues del envio.
- Animacion de espera antes del primer token y brillo/cursor mientras la respuesta sigue en streaming.
- Preparacion visual para tool calls: si el modelo devuelve `tool_calls`, la web muestra nombre y parametros.
- Ejecucion real de tools internas con retorno automatico al modelo: loop de hasta 3 rondas, mensajes `role: tool` con campo `name`.
- Tool `web_search` con DuckDuckGo (sin API key).
- Tool `read_file` restringida al workspace (sin symlink escape, limite 1 MiB, rechaza binarios; lista directorios).
- Timeout de 60s y auditoria basica (`log.Printf`) en ejecucion de tools.
- Eventos SSE `tool_start` y `tool_result` para mostrar ejecucion y resultados en la web.
- Tests unitarios para config, cliente Ollama, capacidades y generacion de docs.
- Documentacion local generada en `docs/ollama-reference.md`.
- Inventario local generado en `docs/local-model-inventory.md`.
- Snapshot cacheado generado en `docs/probe-cache.json`.
- Persistencia de probes individuales: cada ejecucion de `probe chat/tools/json/vision/thinking/embeddings/audio` graba un `ProbeRun` (nombre, modelo, estado, detalles, timestamp) en el campo `probe_runs` del snapshot JSON, haciendo upsert por `name+model`.
- Web validada en `http://localhost:8080`.
- Router de modelos por rol: modelo main (fallback), mas modelos opcionales dedicados para vision, audio y embeddings. Seleccion por botones en el modal de modelos. Si el main ya tiene la capacidad no hace falta asignar uno dedicado; si no hay ninguno con esa capacidad, el control de adjunto se oculta. Persistido en `.env` (`OLLAMA_MODEL_VISION`, `OLLAMA_MODEL_AUDIO`, `OLLAMA_MODEL_EMBED`) y en localStorage. Badges de rol en la barra de capacidades cuando el modelo dedicado difiere del main.
- Drag & drop de archivos sobre la web: imagenes y audio se aceptan si el modelo activo (o el dedicado de rol) soporta esa capacidad; de lo contrario se ignoran silenciosamente. Highlight visual con borde punteado mientras se arrastra.
- Pre-analisis de media por modelo de rol: si hay un modelo dedicado distinto del main para vision o audio, el backend (`internal/router`) lo invoca con un prompt de analisis detallado antes de llamar al main. El resultado textual se inyecta como contexto prefijado en el mensaje (`[image analysis]` / `[audio analysis]`). El main recibe solo texto enriquecido y responde con el historial completo. Si no hay modelo dedicado, el objeto pasa directo al main como antes. Transparente para el frontend.
- Sidebar de sesiones en la web: panel lateral izquierdo ocultable con lista de sesiones previas. Cada sesion es una carpeta (`sessions/{id}/`) con `session.json` (metadata), `messages.json` (mensajes) y `attachments/` (archivos binarios extraidos de base64). La ruta de sesiones es configurable via `SESSIONS_PATH` (default `sessions`, relativo al ejecutable). Se puede crear una nueva sesion, cambiar entre sesiones, y el estado se guarda automaticamente al finalizar cada respuesta del modelo.
- Barra de contexto con porcentaje estimado de uso del context window del modelo main: calcula tokens aproximados (caracteres / 4) sobre `context_length` del modelo activo. Cambia de color a naranja (>70%) o rojo (>90%).
- Memoria a largo plazo local (RAG): sistema de semantic search usando el modelo definido en `OLLAMA_MODEL_EMBED`. Persiste en `memory.jsonl` dentro de una carpeta configurable (`MEMORY_PATH`, default `memory`, relativo al ejecutable). Cada entrada tiene texto, embedding vector, source y timestamp. La busqueda usa cosine similarity en memoria (O(n) eficiente para uso local). El agente gestiona su memoria de forma autonoma via tools: `memory_add` (almacena), `memory_search` (recupera), `memory_delete` (elimina desactualizado), `memory_list` (revisa lo guardado). Un system prompt inyectado en cada conversacion le da criterio: ser proactivo almacenando hechos importantes, buscar cuando se beneficie del contexto pasado, consolidar borrando versiones viejas y guardando nuevas, y priorizar informacion util. Los resultados de tools se devuelven al modelo como tool result para que decida como usarlos. Endpoints REST: `GET /api/memory`, `POST /api/memory`, `POST /api/memory/search`, `DELETE /api/memory/{id}`.

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

- Agregar tests browser completos para upload/paste cuando el runtime exponga carga de archivos.
- Confirmacion de acciones riesgosas para tools (ej. borrar/modificar archivos, ejecutar comandos).
- ~~Agregar indexacion automatica de mensajes de chat a la memoria RAG~~: descartado. Las sesiones ya persisten el historial completo de cada conversacion. La memoria RAG se reserva para informacion con utilidad futura, gestionada manualmente por el agente via `memory_add` con criterio propio.

## Riesgos y Notas

- Algunos modelos reportan capacidades que pueden fallar en prompts reales; por eso los probes end-to-end son necesarios.
- El probe de chat con `qwen3:8b` respondio `ok /think`; el transporte funciona, pero hay que limpiar tokens de control si aparecen en respuestas finales.
- El acceso a `192.168.0.121:11434` no se valido desde el sandbox en la primera exploracion; queda cubierto por `OLLAMA_BASE_URL`, pero requiere prueba en entorno normal.
- La web usa datos vivos cuando Ollama responde y puede caer al snapshot cacheado si el inventario vivo falla.
