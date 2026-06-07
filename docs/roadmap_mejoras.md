# Roadmap y Seguimiento de Mejoras - OllamaBot

Este documento recopila el feedback del usuario sobre la auditoría técnica de OllamaBot y define las acciones de desarrollo para futuras iteraciones del proyecto.

---

## 🛠️ Plan de Seguimiento y Acciones Pendientes

### 1. Gestión de Modelos y Carga en Caliente (UI Web & Telegram)
- **Punto del Relevamiento:** El snapshot y el inventario estático no se actualizan dinámicamente si se descarga/instala un nuevo modelo en Ollama mientras el servidor está activo.
- **Feedback del Usuario:** *"es buena idea ponerle un comando `/reloadmodels` y un boton en la web para recargar los modelos de ollama"*
- **Acción:**
  - [x] Añadir botón de **"Recargar Modelos"** en el diálogo de modelos de la Web.
  - [x] Implementar el comando `/reloadmodels` en el Bot de Telegram.
  - [x] Crear endpoint HTTP para forzar la actualización del inventario y guardado de snapshot.

### 2. Detección de Video
- **Punto del Relevamiento:** Falta probe automático para video (propuesto mediante extracción de frames y análisis por visión).
- **Feedback del Usuario:** *"no lo veo necesario meterse con video, ignoremos esto por ahora"*
- **Acción:**
  - [x] **Descartado:** No se implementará soporte de video en las fases inmediatas.

### 3. Inestabilidad en Cargas de Audio
- **Punto del Relevamiento:** Caídas de Ollama (Error 500) con ciertos modelos (ej. `gemma4:e2b`) al procesar payloads de audio WAV.
- **Feedback del Usuario:** *"esto se puede solucionar reintentando?"*
- **Acción:**
  - [x] **Resuelto:** Investigado y determinado que los errores son deterministas por incompatibilidad de arquitectura/VRAM (el ruteo de audio a modelos dedicados de transcripción mitiga el problema de raíz sin requerir reintentos).

### 4. Detección de Bucles de Herramientas y Manejo de Errores
- **Punto del Relevamiento:** Bucles repetitivos de herramientas cuando el modelo pequeño se equivoca de forma continua.
- **Feedback del Usuario:** *"seria bueno q el agente reciba el mensaje de error de la herramienta para que trabaje sabiendo q error cometio y lo arregle, esto es algo fugaz en su memoria de corto plazo"*
- **Acción:**
  - [x] Validar que los errores devueltos por herramientas (compilación, formato exacto en `Edit`, etc.) se inyecten de forma estructurada e inmediata como un mensaje de rol `tool`. Esto permitirá al modelo usar su contexto de corto plazo para autocorregirse.

### 5. Limpieza de Tokens residuales de Thinking
- **Punto del Relevamiento:** Presencia de tokens de control residuales (ej. `<think>`, `<thought>`) en los mensajes finales del modelo en el chat.
- **Feedback del Usuario:** *"claro"*
- **Acción:**
  - [x] Implementar un limpiador Regex antes de transmitir texto final por SSE o enviar mensajes a Telegram. (Util `agent.CleanThinkingTokens` + filtro de streaming `agent.StreamThinkingFilter` aplicados en el loop del agente, SSE y Telegram.)

### 6. Alternativas de Búsqueda Web
- **Punto del Relevamiento:** La búsqueda actual depende de scraping básico de DuckDuckGo sin API key (riesgo de rate limits y bloqueos).
- **Feedback del Usuario:** *"seria bueno conseguir mas alternativas a esto."*
- **Acción:**
  - [x] Incorporar soporte configurable mediante `.env` para proveedores adicionales (ej. API de SearXNG local, Brave Search API, Tavily, o Google Custom Search).

### 7. Herramienta de Edición Robusta (Edit)
- **Punto del Relevamiento:** `Edit` por reemplazo exacto suele fallar con modelos locales pequeños debido a problemas menores de indentación o espacios.
- **Feedback del Usuario:** *"buena idea"* (sobre cambiar a diffs unificados o reemplazo difuso).
- **Acción:**
  - [x] Desarrollar una herramienta complementaria de edición basada en diffs unificados (formato patch) o un algoritmo de reemplazo difuso.

### 8. Manejo de Aprobaciones y Timeouts
- **Punto del Relevamiento:** Las peticiones de aprobación expiran de forma abrupta a los 5 minutos, interrumpiendo el flujo.
- **Feedback del Usuario:** *"claro, se puede mejorar"*
- **Acción:**
  - [x] Implementar un estado de suspensión en el loop del agente que permita guardar temporalmente el estado del turno para reanudarlo asíncronamente cuando el usuario responda, eliminando el timeout rígido de 5 minutos.

### 9. Indexación Vectorial para RAG Semántico
- **Punto del Relevamiento:** La búsqueda RAG lineal O(n) sobre `memory.jsonl` no escalará si se acumulan miles de registros.
- **Feedback del Usuario:** *"seria muy bueno esto"* (sobre indexación HNSW local o SQLite-vss).
- **Acción:**
  - [ ] Evaluar y migrar el sistema de memoria semántica local a una base de datos embebida con soporte vectorial (ej. SQLite con `sqlite-vss` o biblioteca HNSW nativa en Go).

### 10. Prompting de Memoria y Consolidación RAG
- **Punto del Relevamiento:** Tendencia del agente a duplicar recuerdos o no borrar información obsoleta.
- **Feedback del Usuario:** *"esto seria muy bueno tambien"* (sobre prompts de sistema restrictivos y criterios de consolidación).
- **Acción:**
  - [ ] Refinar las instrucciones de sistema de memoria para forzar al agente a buscar primero y consolidar información (borrar registro antiguo antes de añadir el nuevo).
  - [ ] Implementar lógica interna de desduplicación por similitud umbral en el backend de memoria.

### 11. Integración en Telegram Bot (Comandos)
- **Punto del Relevamiento:** Telegram solo cuenta con `/start` y `/new`, sin acceso a configuraciones, memoria ni proyectos.
- **Feedback del Usuario:** *"si, faltan comandos pero se iran agregando poco a poco"*
- **Acción:**
  - [ ] Diseñar y añadir progresivamente comandos:
    - `/status` para monitorizar VRAM y estado de Ollama.
    - `/settings` para cambiar el modelo activo.
    - `/projects` para consultar las tareas en curso.
    - `/memory` para realizar consultas a la memoria semántica.

### 12. Latidos de Proyectos (Heartbeat Ticker)
- **Punto del Relevamiento:** Conflictos de procesamiento cuando múltiples proyectos compiten por Ollama en un mismo latido de ticker.
- **Feedback del Usuario:** *"hay q pensarlo mas"*
- **Acción:**
  - [ ] Mantener el ticker actual de procesamiento secuencial simple y reevaluar la cola/paralelismo en etapas posteriores.

### 13. Sleep Mode y Consumo de GPU (Subagentes)
- **Punto del Relevamiento:** El refinamiento en segundo plano de Sleep Mode puede ralentizar el equipo de forma imprevista si el usuario está usando otra app pesada.
- **Feedback del Usuario:** *"si, la idea de subagentes es tentadora"*
- **Acción:**
  - [ ] Diseñar la infraestructura del Reflector de Sleep Mode para que actúe como un subagente de baja prioridad o con límites de CPU/GPU, comprobando la carga de hardware antes de iniciar la iteración.

### 14. Seguridad en la Consola Web
- **Punto del Relevamiento:** La consola expuesta en LAN no tiene autenticación, permitiendo manipulación de archivos del workspace a terceros.
- **Feedback del Usuario:** *"si, se podria poner un pass por .env para la web"*
- **Acción:**
  - [ ] Implementar autenticación básica mediante una variable de entorno `WEB_PASSWORD` (o similar) y una pantalla sencilla de login en la SPA.

### 15. Paneles Visuales de Recursos Persistentes
- **Punto del Relevamiento:** La interfaz web carece de visores gráficos para el catálogo de Skills y los registros de Memoria Semántica.
- **Feedback del Usuario:** *"claro, la web podria tener paneles q telegram no tiene acceso, como skills y las memorias."*
- **Acción:**
  - [ ] Añadir paneles dedicados en la Consola Web para:
    - **Skills:** Ver, editar y crear archivos `SKILL.md` directamente desde la consola.
    - [x] **Memoria Semántica:** Explorador visual del RAG, con capacidad de buscar y borrar entradas de memoria persistentes, así como re-indexar vectores manualmente tras cambiar de modelo de embeddings.
