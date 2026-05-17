# OllamaBot Docs

Esta carpeta guarda el estado vivo del proyecto: lo hecho, lo pendiente, la referencia de Ollama y las pruebas ejecutadas contra la instancia local.

## Documentos

- [progress.md](progress.md): bitacora de avance, decisiones tomadas y proximos pasos.
- [ollama-usage.md](ollama-usage.md): guia practica de uso de la API de Ollama para este proyecto.
- [probe-results.md](probe-results.md): pruebas manuales ejecutadas y resultados observados.
- [probe-cache.json](probe-cache.json): snapshot cacheado de resultados esperados/modelos para no reprobar todo cada vez.
- [ollama-reference.md](ollama-reference.md): referencia generada con payloads minimos y links fuente.
- [local-model-inventory.md](local-model-inventory.md): inventario generado desde `/api/tags` y `/api/show`.

## Estado Actual

La fase 1 esta implementada en Go. El sistema ya puede:

- Leer configuracion desde `.env`.
- Conectarse a Ollama via REST.
- Listar modelos instalados.
- Consultar metadata y capacidades con `/api/show`.
- Ejecutar probes de chat, tools, JSON estructurado, vision, thinking, embeddings y audio experimental.
- Generar esta documentacion local.
- Guardar un snapshot cacheado de modelos/resultados esperados.
- Servir una web local para ver modelos, capacidades, memoria y chatear con un modelo principal.

Telegram y agente autonomo quedan para fases posteriores.
