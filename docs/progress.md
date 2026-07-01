# Project Progress

## General Objective

Build a modular autonomous agent based on local Ollama models. The agent detects installed models, their capabilities, and uses those capabilities as appropriate. The supported channels are Telegram and a local web UI.

## Phase 1 — Implemented

A Go foundation was implemented focused on documentation and verifiable probes.

Done:

- Go project created with module `github.com/jonathanhecl/ollamabot`.
- Main CLI in `cmd/ollamabot`.
- Configuration from `.env` and environment variables.
- Native HTTP client for Ollama in `internal/ollama`.
- Capability mapping in `internal/capabilities`.
- Probes in `internal/probe`.
- Markdown generator in `internal/docs`.
- JSON cache of expected results in `internal/cache`.
- Local web server in `internal/web`.
- `.env.example` with initial keys.
- Normal execution without parameters: loads `.env` from the executable folder, creates it interactively if missing (web, port, exposure, password, Telegram token), and starts web and/or Telegram per configuration.
- Ollama URL configuration from the web UI, persisted to `.env`.
- Models modal in the web UI instead of a permanent table.
- Web chat with SSE streaming from Ollama.
- Incremental display of `thinking` in a separate block.
- Multimodal attachments from file or paste: images and audio use Ollama's multimodal payload.
- Image/audio previews before sending and inside the chat after sending.
- Waiting animation before the first token and glow/cursor while the response is still streaming.
- Visual preparation for tool calls: if the model returns `tool_calls`, the web shows name and parameters.
- Real execution of internal tools with automatic return to the model: loop of up to 3 rounds, `role: tool` messages with `name` field.
- `web_search` tool with DuckDuckGo (no API key required).
- `read_file` tool restricted to the workspace (no symlink escape, 1 MiB limit, rejects binaries; lists directories).
- 60s timeout and basic audit logging (`log.Printf`) on tool execution.
- SSE events `tool_start` and `tool_result` to show execution and results in the web UI.
- Unit tests for config, Ollama client, capabilities, and docs generation.
- Local documentation generated in `docs/ollama-reference.md`.
- Local inventory generated in `docs/local-model-inventory.md`.
- Cached snapshot generated in `docs/probe-cache.json`.
- Individual probe persistence: each `probe chat/tools/json/vision/thinking/embeddings/audio` run saves a `ProbeRun` (name, model, status, details, timestamp) in the `probe_runs` field of the JSON snapshot, upserting by `name+model`.
- Web validated at `http://localhost:8080`.
- Model router by role: main model (fallback) plus optional dedicated models for vision, audio, and embeddings. Selection via buttons in the models modal. If the main model already has the capability, no dedicated one is needed; if no model has that capability, the attachment control is hidden. Persisted in `.env` (`OLLAMA_MODEL_VISION`, `OLLAMA_MODEL_AUDIO`, `OLLAMA_MODEL_EMBED`) and localStorage. Role badges in the capabilities bar when the dedicated model differs from main.
- Drag & drop of files onto the web UI: images and audio are accepted if the active model (or the dedicated role model) supports that capability; otherwise they are silently ignored. Visual highlight with dotted border while dragging.
- Media pre-analysis by role model: if there is a dedicated model different from main for vision or audio, the backend (`internal/router`) invokes it before calling main. See `docs/media-routing.md` for the full flow, decision matrix, and current injection format.
- Sessions sidebar in the web UI: collapsible left panel with a list of previous sessions. Each session is a folder (`sessions/{id}/`) with `session.json` (metadata), `messages.json` (messages), and `attachments/` (binary files extracted from base64). The sessions path is configurable via `SESSIONS_PATH` (default `sessions`, relative to the executable). You can create a new session, switch between sessions, and state is saved automatically at the end of each model response.
- Context bar with estimated percentage of the main model's context window usage: calculates approximate tokens (characters / 4) over the active model's `context_length`. Changes color to orange (>70%) or red (>90%).
- Long-term local memory (RAG): semantic search system using the model defined in `OLLAMA_MODEL_EMBED`. Persists in `memory.jsonl` inside a configurable folder (`MEMORY_PATH`, default `memory`, relative to the executable). Each entry has text, embedding vector, source, and timestamp. Search uses cosine similarity in memory (O(n), efficient for local use). The agent manages its memory autonomously via tools: `memory_add` (store), `memory_search` (retrieve), `memory_delete` (remove outdated), `memory_list` (review stored). A system prompt injected into each conversation gives it criteria: be proactive about storing important facts, search when it benefits from past context, consolidate by deleting old versions and storing new ones, and prioritize useful information. Tool results are returned to the model as tool results for it to decide how to use them. REST endpoints: `GET /api/memory`, `POST /api/memory`, `POST /api/memory/search`, `DELETE /api/memory/{id}`.
- Confirmation of risky tool actions (Write/Edit) integrated in both the Web interface (via SSE and modal) and Telegram (using interactive inline keyboards), with automatic omission for edits within the 'workspace' directory.
- Model management and hot reload: "Reload Models" button in the Web UI, `/reloadmodels` command in Telegram, and HTTP endpoint to update inventory and save snapshot.
- Web search alternatives: configurable support for Brave Search API, Tavily Search API, and DuckDuckGo with reorderable provider priority in the Web UI.
- Loop detection and error handling: structured injection of tool execution errors into `tool` role messages to allow self-correction and avoid repetitive loops.
- Residual thinking token cleanup: removal of reasoning tags (`<think>`, `<thought>`, etc.) via regex in final responses and incremental filtering in SSE streaming.
- Robust edit tool: replacement with fuzzy-match fallback for lines and unified diff generation in the `Edit` tool.
- Async approval management: suspension in the agent loop to wait for approval responses in Web (SSE) and Telegram (inline buttons) without rigid 5-minute timeouts.
- Memory consolidation and deduplication: 0.85 cosine similarity threshold and exact text matching to prevent duplicates in RAG memory.
- Telegram bot command integration: `/status` (VRAM and Ollama state), `/settings` (model switching), `/projects` (active tasks), and `/memory` (RAG search).
- Sleep mode and GPU safety: sequential sleep mode reflector in background that checks hardware load on Ollama (`/api/ps`) before starting iteration to avoid slowing down the machine.
- Web console security: basic authentication via `X-Web-Password` header and `WEB_PASSWORD` environment variable with login screen in the SPA.
- Semantic memory visual explorer: panel in the web console to search, add, delete, and manually re-index RAG vectors after changing the embeddings model.
- Timestamps in messages: automatic timestamp recording in chat messages and time bubble display in the bottom-right corner of each message, similar to Telegram.
- Session action alignment and grouping: rename (pencil) and delete (x) buttons placed in a `.session-actions` container to the right of each sidebar item, horizontally aligned with a unified 22x22px design and optimized hover effects.
- Connection and status monitoring: continuous checking (every 5 seconds) against the Go server and Ollama, updating visual status to red/offline when there's no response or the server stops.
- API key preservation on disable: both frontend and server prevent Tavily or Brave Search API keys from being deleted when disabling the provider or unchecking web search. The key remains safely stored for when the feature is re-enabled.
- Copy message and code buttons: interactive copy buttons integrated. On hover over a message, a `📋` button appears in the top-right corner to copy the full markdown text. If the response contains code blocks, a premium header is generated showing the programming language and a dedicated copy button.
- Real-time Telegram and Web UI sync: automatic expiration of Telegram sessions after 30 minutes of inactivity, auto-generation of titles after the first exchange, and background polling in the Web UI every 2 seconds to sync the session list and update messages in real time.
- Telegram session commands: `/sessions` (list the last 10 sessions with titles and IDs) and `/session <ID>` (switch to an active session and recover previous conversation context).
- Sessions configuration panel: new "Sessions" tab in the web settings dialog, grouping the auto-rename session checkbox and a text input to customize Telegram session expiration in minutes (default 30). Title generation tasks for auto-rename now delegate to the subagent model (or main if not configured), both in web and Telegram.
- Auto-RAG and configuration centralization: proactive user memory retrieval via pre-querying embeddings of the user's message against the RAG database on each query (with 0.70 similarity threshold). The injection and dynamic reload flow of SOUL.md and USER_PROFILE.md was centralized in the agent's main execution loop (Agent.Run), eliminating duplicated logic in Web, Telegram, and AutonomousManager channels, allowing the agent to perceive its own profile modifications in real time within the same turn.
- Proactive Telegram notifications: connection of background task completions from `AutonomousManager` via a global `OnTaskCompletion` callback injected in `Start` and processed in Telegram to proactively notify the user after success or failure of their mini-projects.
- Frontend integration tests (browser): creation of automated scripts based on Node + Playwright (`tests/browser/`) that test the full authentication flow (login), drag & drop of media files, message sending, and copying of responses and code blocks in the Web UI.
- Event loop blocking fix and event mocking: fix for a critical event loop blocking caused by synchronous subprocess executions in the test script, migrating to async executions (`spawn`). Fixed a selector error (`.attachment-preview` vs `.attachment`) in the file drag test, and implemented a safe `DataTransfer` mock using `Object.defineProperty` on generic events.
- /goal command and persistent autonomous execution: implementation of the /goal command allowing the user to set a persistent goal contract in the chat session (with `/goal <objective>`, `/goal pause`, `/goal resume`, `/goal clear`, and `/goal` for status). The agent executes iterations autonomously in the background (limited to 20 cycles), notifying the user in real time and running progress evaluation with subagents in each cycle until the goal is fulfilled.
- Telegram audio visualization in Web UI: fix in the Telegram bot to correctly save and map message attachments (`Attachments` and `ImageKinds`) in session history, ensuring WAV audio (generated by conversion) is persisted and plays consistently in the web chat interface.
- Real-time sync via SSE (EventSource): implementation of a real-time event channel `/api/events` on the web server and a global subscription/notification system (`sessions.NotifyUpdate`) to instantly sync Telegram activities (user messages, AI responses, and goal changes) and the web interface without relying solely on slow periodic polling or requiring manual page refresh.
- Unified media pipeline with structured transcription (see `docs/media-routing.md`): `router.ResolveMessages` replaces duplicated logic from web (`resolveMedia`) and Telegram (`resolveTelegramMedia`). Audio transcription is ALWAYS extracted (even in passthrough) using structured JSON output (`format` + schema + `temperature: 0`) with fields `transcription`, `language`, `sounds`, `unreadable`. Routing decision is based on real capabilities from the probe snapshot (`cache.Checker`): main with capability → passthrough; dedicated different → route; nobody → discard without error with note. Transcriptions are persisted in `AttachmentMeta.Transcription` (shared sessions web/Telegram) and displayed under each audio player in the web; Telegram does not show them to the user. Audio base64 is never re-sent to the model in subsequent turns (replaced by transcription) and the SSE `media_pre_processing` event is now structured JSON per attachment.
- Strict chronological session format (timeline refactor): each agent turn generates an independent `assistant` message with internal `steps` in chronological order (`thinking` → `tool_call` → `tool_result` → `content`). Consecutive assistant merging was eliminated in both web (`groupMessagesAndTools`) and Telegram (`telegramStreamHandler`). The web frontend detects turn changes via the `assistant_turn` SSE event. Media pre-processing results are no longer injected as separate `assistant` messages in the session; instead, the original `user` message's attachments are enriched with `processed_by`, `processed_at`, `description`/`transcription` metadata. The `RawMsg` schema now includes mandatory traceability fields: `model`, `timestamp`, `channel`, `type`, `steps`, and `metrics` (per turn). `AttachmentMeta` was extended with `processed_by`, `processed_at`, and `description`. In Telegram, `telegramStreamHandler` accumulates `steps` and `metrics` per turn using `turnSnapshot` and distributes them to each `assistant` message on persist.
- Unified agent and session pipeline: web and Telegram now delegate `agent.Run` events to `sessions.Recorder`, which is the single source of truth for persisting `steps`, `metrics`, partial snapshots, and final saves. Thinking is always saved as `steps[{type:"thinking"}]` for both channels and displayed in real time on the web; Telegram continues sending only the clean final response and brief tool notifications to the chat. The web persists the user message before streaming and reloads the session on completion, so the server maintains the canonical format just like Telegram. The `saveGen` guard prevents stale streaming snapshots from overwriting the final save.
- Markdown horizontal rules (`***`, `---`, `___`): rendered as `<hr>` in the web and as a visual line in Telegram.
- Visual response grouping in the web: reimplementation of consecutive assistant message grouping in the Web UI (in `groupMessagesAndTools`), combining their contents, execution steps, and performance metrics (duration, prompt and response tokens), and recalculating the overall average tokens per second. The session and database retain messages as separate, structured per-turn entries.
- Approved plans as autonomous contracts: `SessionPlan` now supports `deferred` state, `deferred_until`, follow-up summary, and `last_progress_at`; the agent loop requires real tools before `complete_plan_step`, retries plain-text responses while there are active steps, and can only terminate with a completed or explicitly deferred plan. Added `defer_plan_continuation`, `PlanMonitor` for resuming deferred or stalled plans, fixed progress checklist in the web, compact notices in Telegram, and tests for multi-step execution, defer, and resumption.
- Unified session permissions and loops: tool approvals are now persisted in the session as `pending_approval`, can be resolved from Web or Telegram, and support session grants to repeat identical commands without re-requesting permission. `execute_command` normalizes duplicate arguments and the agent loop cuts on detecting repetitive calls without progress.
- Risk-based autonomous permissions: active plans, active goals, and background resumptions use an autonomous policy that executes safe commands without asking permission and only interrupts with approval if the risk classifier detects real risk. Telegram requests include the command line to execute and a risk summary. XML fallback now recognizes lowercase tool tags like `<execute_command>`.


Available commands:

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
go run ./cmd/ollamabot serve --port 8080 --cache docs/probe-cache.json
```

## Decisions Made

- Stack: Go.
- First phase: documentation + probes, before full Telegram/web/agent implementation.
- No external dependencies; custom `.env` parser and CLI based on `flag`.
- Capabilities reported by `/api/show.capabilities` are considered `confirmed`.
- Live memory consumption comes from `GET /api/ps`, specifically `size_vram` for loaded models.
- Encoders seen in `projector_info`, such as audio or vision, are considered `inferred` if there's no end-to-end test.
- Audio remains experimental until a stable REST payload is confirmed.
- `gemma4:e2b` reports `audio` in `/api/show.capabilities`, but local end-to-end testing with WAV caused the Ollama runner to crash with error 500.

## Pending

- Add complete browser tests for upload/paste when the runtime exposes file loading.
- ~~Confirmation of risky tool actions~~: completed in both web and Telegram with omission in the 'workspace' directory.
- ~~Add automatic indexing of chat messages to RAG memory~~: discarded. Sessions already persist the full conversation history. RAG memory is reserved for information with future utility, managed manually by the agent via `memory_add` with its own criteria.

## Risks and Notes

- Some models report capabilities that may fail on real prompts; that's why end-to-end probes are necessary.
- The chat probe with `qwen3:8b` responded `ok /think`; transport works, but control tokens need to be cleaned if they appear in final responses.
- Access to the configured Ollama base URL was not validated from the sandbox in the first exploration; it's covered by `OLLAMA_BASE_URL`, but requires testing in a normal environment.
- The web uses live data when Ollama responds and can fall back to the cached snapshot if the live inventory fails.

## Fix: repetitive plan mode (2026-06-19)

- The agent loop now detects an active approved plan and stops reinforcing `present_plan` on each iteration.
- `present_plan` rejects duplicate calls when there's already an active plan in the session.
- `NormalizePlanSteps` splits malformed numbered lists into individual steps.
- Web UI: a single live progress widget; the recorder updates the plan step instead of accumulating blocks.

## Fix: generated image replaces progress (2026-06-19)

- `AddOrUpdateImageStep` updates the same step on completion (not only when status=running).
- `FinalizeSteps` preserves done/error status on `image_progress` steps with imageURL.
- Web UI: deduplicates generated image vs attachment; reconstructs imageURL on session reload.
- After F5: `ResolveSessionMessages` cleans `image_progress` placeholders without final image; merges attachments when grouping assistant messages.

## Session ordering by last message (2026-06-19)

- The session list is sorted by `last_message_at` (timestamp of the last message), not by the file's `updated_at`.

## Session list cache (2026-06-19)

- `Store` preloads an in-memory index on initialization (`warmListCache`).
- `List()` serves from cache without re-reading disk; `Save`/`Delete` update only the affected entry.
- Lightweight endpoint `GET /api/sessions/{id}/entry` and SSE `session_updated` refresh a single session in the UI.

## Web Skills Explorer (2026-06-19)

- REST API `/api/skills` to list, view, edit, and delete skills from the configured directory.
- **Skills** button in the Web UI with list, detail, and edit modals (Memory Explorer pattern).

## Synchronized agent state web/Telegram (2026-06-24)

- `sessions.MarkProcessing` / `MarkIdle` track active agent turns (engine, PlanMonitor, GoalManager).
- The session API exposes `agent_busy`; SSE emits `agent_status` on each change.
- The web shows `processing...` / `awaiting approval` in the status bar and spinner in the timeline when Telegram or background tasks are executing tools.

## Hidden internal messages in web (2026-06-24)

- Internal loop prompts (plan monitor, plan/TODO enforcement) are no longer persisted or shown as bubbles in the web timeline.

## User Feedback Loop + Duplicate Skill Prevention (2026-06-28)

- **Global text feedback**: new `internal/learning/feedback.go` with `FeedbackEntry`, `SaveFeedback`, `LoadFeedback`, `ClearFeedback`. Stored in `sessions/feedback.json`.
- **Web API**: `POST /api/feedback` accepts `{ text, category }` (correction/preference/praise).
- **Telegram**: `/feedback <text>` command saves feedback as correction.
- **Web UI**: Feedback button + dialog with category dropdown and textarea.
- **Sleep manager**: loads text feedback in `runLearningCycleForSessionsWithModel`, appends to analysis prompt as `## User Text Feedback`, clears after successful reflector run. System prompt updated to prioritize explicit user feedback.
- **Duplicate skill prevention**: `CreateSkill` now checks existing skills for name similarity (Levenshtein ratio >= 0.8) and description similarity (Jaccard index >= 0.6). Blocks creation and suggests `skill_edit` instead.
- Tests: `feedback_test.go` (save/load/clear round-trip), `skills_tools_test.go` (duplicate name block, duplicate description block, no-similar success).

## Tool Naming Convention + New Tools (2026-06-28)

- **Snake_case rename**: `Write` → `write_file`, `Edit` → `edit_file`, `TodoWrite` → `todo_write` across all code, tests, XML fallback aliases, approval, risk, rescue, Telegram display, and README.
- **4 new tools**:
  - `search_files`: regex search across workspace files with glob filtering and max results. Read-only, no approval.
  - `list_files`: directory listing with recursive and glob filter options. Read-only, no approval.
  - `list_code_definitions`: extracts function/method/type/struct/interface/constant names from Go files via `go/ast`. Read-only, no approval.
  - `apply_diff`: applies unified diff format to files. Requires approval (same as write_file/edit_file).
- **Parameter improvements**:
  - `read_file`: added `offset` (1-indexed line) and `limit` (max lines) for paginated reading of large files.
  - `write_file`: added `append` boolean to append to existing files instead of overwriting.
  - `execute_command`: added `timeout` (seconds, default 60) replacing the hardcoded timeout.
- **Supporting changes**: XML fallback aliases for all 4 new tools, Telegram display formatting, agent loop error recovery for `apply_diff`, risk classification, approval signature, rescue path param mapping.
- All tests pass, build clean.

## Current Time Context Injection (2026-06-28)

- **Temporal awareness**: the agent loop now prepends a system message with the current date, time, and UTC offset at the start of every iteration in `internal/agent/loop.go`. Uses `time.Now()` from the system clock — no config or `.env` changes needed.
- **Format**: `Current date and time: Sunday, June 28, 2026 at 11:16 PM (UTC-03:00)` — human-readable, includes day of week.
- **Why context injection over a tool**: zero latency, always available, ~20 tokens, follows the existing pattern of dynamic system messages (SOUL, profile, skills, todos).
- Test: `TestTimeContextMessageFormat` in `internal/agent/loop_test.go`.
