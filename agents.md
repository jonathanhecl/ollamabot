# Agents

Guidelines for humans and coding agents working on this repository.

## Objective

Modular autonomous agent on **local Ollama only**. The product is the **ollamabot process** (`cmd/ollamabot`):
configuration, chat engine, tools, sessions, sleep, goals, and optional background projects.
**Web** and **Telegram** are channels—they adapt I/O and UX; they must not own chat logic or
runtime model policy. The agent service requires **at least one** channel enabled (`SERVER_ENABLED`
or `TELEGRAM_BOT_TOKEN`). Running with neither fails at startup; `probe` subcommands are the
exception (Ollama checks without starting web or Telegram).

## Architecture

```text
Channels (transport only)
  web/server.go     → HTTP, SSE, REST, static UI
  telegram/bot.go   → polling, keyboards, media download, delivery
        │
        ▼
  engine.ProcessTurn   ← single entry for every chat turn (web + telegram)
        │
        ├── config.ResolveModel (from .env, not from client)
        ├── router.ResolveMessages (vision/audio)
        ├── engine.InjectContext (uploads + attachments)
        ├── tools.Registry (handlers injected per channel)
        ├── agent.Run (loop, tools, streaming)
        └── sessions.Recorder + optional AutoNameSession
```

Background services started once in `cmd/ollamabot/main.go` and shared by channels:

| Service | Package | Notes |
|---------|---------|--------|
| Sleep manager | `internal/learning` | Runs without web open; `OnSleepActivity` from engine |
| Goal manager | `internal/agent/goal.go` | Per-session objectives; API + Telegram `/goal` |
| Autonomous projects | `internal/agent/autonomous.go` | Workspace projects; web UI + notifications |

## Design principles

- **Modular**: chat, vision, audio, tools, embeddings are separate concerns with role-specific models.
- **Local-first**: all inference via local Ollama. No external LLM APIs.
- **Graceful fallback**: if no model supports a capability, that feature is disabled silently for users.
- **Server-authoritative config**: runtime models and roles come from `config.Config` (`.env`).
  Channels must not send or override `OLLAMA_DEFAULT_MODEL` on chat requests.
- **No agent-global conversation state**: history lives in `internal/sessions` keyed by session ID
  (web session or Telegram `chat_id` mapping). The agent loop is stateless between turns.

## Model router

Role assignment is centralized in `config.ResolveModel(cfg, role)` (`internal/config/models.go`).
The media router (`internal/router`) uses the same config for vision/audio/image steps.

| Role | Env | When used | Fallback |
|------|-----|-----------|----------|
| `main` | `OLLAMA_DEFAULT_MODEL` | Text, history, tools, final response | required |
| `vision` | `OLLAMA_MODEL_VISION` | Image attachments | main if it has vision, else feature off |
| `audio` | `OLLAMA_MODEL_AUDIO` | Audio attachments | main if it has audio, else feature off |
| `subagent` | `OLLAMA_MODEL_SUBAGENT` | Session titles, summarization | main |
| `learning` | `OLLAMA_MODEL_LEARNING` | Sleep-mode reflection | main |
| `embed` | `OLLAMA_MODEL_EMBED` | Semantic memory | none |
| `image` | `OLLAMA_MODEL_IMAGE` | Image generation tool | none |

Dedicated vision/audio flow: role model produces text context → injected for **main** → main replies.

## Chat turn lifecycle (`engine.ProcessTurn`)

```text
user input (any channel)
  → notify sleep activity (if enabled)
  → ResolveModel(main) from config
  → router.ResolveMessages (media pre-processing)
  → persist media metadata on session
  → InjectContext (uploads + session attachments)
  → intercept /image command
  → build tools.Registry (channel injects approval/clarify/plan handlers)
  → agent.Run (streaming, tool loop)
  → recorder.FinalizeAndSave
  → AutoNameSession if enabled and default title
  → channel delivers response (SSE or Telegram message)
```

Do not duplicate this pipeline in `internal/web` or `internal/telegram`. Add channel-specific
behavior only at the edges (auth, SSE, keyboards, expiry checks, ffmpeg).

## Internal tools

- Tools are Go functions in `internal/tools`, not model builtins.
- The model requests tools via `tool_calls`; the agent executes and returns results.
- Each tool: name, description, JSON Schema parameters, Go handler.
- Security: per-channel allowlists where needed, timeouts, audit logging.
- Interactive tools (approval, clarification, plan confirmation) use `tools.*Handler` interfaces
  implemented by each channel (SSE blocking vs Telegram inline keyboards).

## Memory and conversations

- Per-session message history in `sessions/` (`session.json`, `messages.json`, `attachments/`).
- Roles: `user`, `assistant`, `tool`; steps and metrics on assistant turns.
- Context trimmed when over model `context_length` (optimization may use subagent model).
- Long-term memory: `internal/memory` + embeddings tool; pre-fetch in `agent.Run`.

## Channels (what belongs where)

### Web (`internal/web`, `internal/web/static/app.js`)

- HTTP/SSE transport, optional password auth
- Settings UI that **edits** `.env` via `POST /api/settings` (not runtime model picker for chat)
- Render timeline, steps, metrics, sessions, memory, projects
- Must call `engine.ProcessTurn`; must not call Ollama for auto-naming or chat model selection

### Telegram (`internal/telegram`)

- Long polling, message chunking, HTML formatting
- Media download and conversion (e.g. voice → WAV)
- Session policies: inactivity expiry, `checkMessagesRelationship`
- Inline keyboards for tool approval/clarification/plan
- Must call `engine.ProcessTurn` after building `router.MediaMessage` list

## Implementation rules

- New domain logic → prefer `internal/engine` or existing shared packages, not channel packages.
- New modules → `internal/<name>`.
- Avoid new external dependencies without justification.
- New capabilities need a probe in `internal/probe` before production use.
- `.env` changes persist via `config.SaveBasic`.
- Record meaningful progress in `docs/progress.md`.
- All **user-facing** UI strings, menus, CLI prompts, logs, and errors shown to users: **English only**.

## Key paths

| Path | Purpose |
|------|---------|
| `cmd/ollamabot/main.go` | Process entry, shared managers, channel startup |
| `internal/engine/` | `ProcessTurn`, context injection, session naming |
| `internal/agent/loop.go` | Agent streaming and tool iteration |
| `internal/web/server.go` | Web API; thin wrapper around engine |
| `internal/telegram/bot.go` | Telegram bot; thin wrapper around engine |
| `.env` / `.env.example` | Runtime configuration |
