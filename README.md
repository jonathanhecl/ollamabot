# ollamabot

Local-first autonomous agent powered by [Ollama](https://ollama.com). One Go process runs the
agent core; **Web** and **Telegram** are transport channels that talk to the same engine. Chat
behavior, model choice, tools, sessions, and background jobs do not depend on which channel you use.

## Features

- **Dual channels**: Web UI (SSE streaming, settings, session browser) and Telegram bot (polling, inline keyboards, media download)
- **20+ agent tools**: web search, file read/write/edit, shell commands, memory, skills, plans, image generation, and more
- **Multimodal**: image and audio attachments with dedicated model routing and structured transcription
- **Long-term memory**: RAG with local embeddings — the agent stores, searches, and consolidates knowledge autonomously
- **Sleep mode**: background learning cycles that reflect on past conversations when the user is inactive
- **Goals**: persistent objectives with autonomous execution loops and progress evaluation
- **Autonomous projects**: background workspace tasks with web dashboard and Telegram notifications
- **Skills**: custom reusable instruction sets the agent can create, edit, and invoke
- **Plan system**: structured multi-step plans with user approval, progress tracking, and deferred continuation
- **Thinking support**: reasoning blocks displayed in real-time when the model supports it
- **Session management**: persisted history, auto-naming, context window indicator, cross-channel sync
- **Security**: per-tool approval system, risk classification, workspace sandboxing, optional web password
- **Zero external dependencies**: pure Go, only requires a local Ollama instance

## Architecture

```text
                    ┌─────────────────────────────────────┐
                    │         cmd/ollamabot (main)        │
                    │  config (.env) · sleep · goals · autonomous │
                    └─────────────────┬───────────────────┘
                                      │
          ┌───────────────────────────┼───────────────────────────┐
          │                           │                           │
          ▼                           ▼                           ▼
   internal/web              internal/engine              internal/telegram
   HTTP · SSE · UI            ProcessTurn (chat core)      polling · keyboards
          │                           │                           │
          └───────────────────────────┼───────────────────────────┘
                                      │
                    ┌─────────────────┴─────────────────┐
                    │ agent · router · tools · sessions │
                    │ memory · probe · config           │
                    └─────────────────┬─────────────────┘
                                      ▼
                              Ollama (local)
```

| Layer | Packages | Responsibility |
|-------|----------|----------------|
| **App engine** | `internal/engine` | Single chat turn: media routing, context injection, tools, recorder, auto-naming |
| **Agent loop** | `internal/agent` | Streaming completion, tool rounds, context optimization, SOUL/profile |
| **Model router** | `internal/router` | Vision/audio pre-processing before the main model |
| **Config** | `internal/config` | `.env` is the runtime source of truth; `ResolveModel(role)` picks models |
| **Web channel** | `internal/web` | Local UI, settings editor, session browser, SSE streaming |
| **Telegram channel** | `internal/telegram` | Bot API, media download, inline approvals, chunked replies |

**Web vs Telegram:** same `engine.ProcessTurn` pipeline. The web UI adds configuration panels,
memory explorer, project dashboard, and richer step/metrics display. Telegram keeps channel-specific
policies (session expiry, relationship checks, ffmpeg for voice). Neither channel chooses the main
model at request time—that comes from `OLLAMA_DEFAULT_MODEL` in `.env`.

See [`docs/media-routing.md`](docs/media-routing.md) for the full multimodal routing flow.

## Channels (pick at least one)

The agent service needs **at least one channel** to run. You can use web, Telegram, or both — but not neither.

| Mode | `.env` | What starts |
|------|--------|-------------|
| Web only | `SERVER_ENABLED=true`, no `TELEGRAM_BOT_TOKEN` | Local UI on `SERVER_PORT` |
| Telegram only | `SERVER_ENABLED=false`, `TELEGRAM_BOT_TOKEN=...` | Bot in foreground |
| Both | `SERVER_ENABLED=true` + `TELEGRAM_BOT_TOKEN=...` | Web server + Telegram in background |

If both are disabled, default startup fails with an error asking you to enable one.

**Without a channel:** only maintenance subcommands work — mainly `probe` for checking Ollama models
and capabilities without starting the agent. Example: `go run ./cmd/ollamabot probe models`.

## Quick start

### Prerequisites

- [Go](https://go.dev/dl/) 1.25+
- [Ollama](https://ollama.com) running locally with at least one model installed

### Setup

Copy [`.env.example`](.env.example) to `.env` and set at least:

```env
OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_DEFAULT_MODEL=your-model:tag
SERVER_ENABLED=true
SERVER_PORT=8080
```

Run with no subcommand (starts web and/or Telegram per `.env`):

```bash
go run ./cmd/ollamabot
```

If `.env` is missing next to the executable, the binary runs an interactive setup (Ollama URL, web server, port, network exposure, password, Telegram token).

**Telegram-only** (no web server):

```env
SERVER_ENABLED=false
TELEGRAM_BOT_TOKEN=your_token
```

**Both channels** (default when both are enabled): web on `SERVER_PORT`, Telegram bot in background.

## Model roles

Configured in `.env` and editable from the web **Manage Models & Roles** UI (writes back to `.env`):

| Role | Env variable | Used for |
|------|----------------|----------|
| Main | `OLLAMA_DEFAULT_MODEL` | Chat, tools, final answers (required) |
| Vision | `OLLAMA_MODEL_VISION` | Image pre-processing when set |
| Audio | `OLLAMA_MODEL_AUDIO` | Audio transcription when set |
| Subagent | `OLLAMA_MODEL_SUBAGENT` | Session titles, context optimization; falls back to main |
| Learning | `OLLAMA_MODEL_LEARNING` | Sleep-mode reflection; falls back to main |
| Embeddings | `OLLAMA_MODEL_EMBED` | Long-term memory search |
| Image gen | `OLLAMA_MODEL_IMAGE` | `generate_image` tool |
| Thinking | `OLLAMA_THINK_ENABLED` | Reasoning phase when main model supports it |

Capabilities are probed via `internal/probe`; missing roles disable features gracefully without user-facing errors.

## Agent tools

The agent has access to the following tools. Risky operations (file writes, shell commands) require user approval via Web modal or Telegram inline keyboard.

| Tool | Description |
|------|-------------|
| `web_search` | Search the web via DuckDuckGo, Brave, or Tavily |
| `fetch_webpage` | Fetch and read raw text content from a URL |
| `read_file` | Read file contents or list directory entries in the workspace |
| `Write` | Write file contents atomically to the workspace |
| `Edit` | Replace exact text in an existing file with fuzzy-match fallback |
| `TodoWrite` | Maintain a live TODO checklist during multi-step tasks |
| `execute_command` | Run shell commands (ffmpeg, python3, etc.) in the workspace |
| `memory_search` | Semantic search over long-term memory via embeddings |
| `memory_add` | Store text in long-term memory for future retrieval |
| `memory_delete` | Delete an outdated memory entry by ID |
| `memory_list` | List recent memory entries |
| `skill_list` | List all installed custom skills |
| `skill_get` | Retrieve raw instructions of a specific skill |
| `skill_create` | Create a new custom skill with frontmatter and checklist |
| `skill_edit` | Modify or merge properties of an existing skill |
| `skill_delete` | Remove a custom skill |
| `present_plan` | Present a structured plan for user approval before complex tasks |
| `complete_plan_step` | Mark a plan step as completed |
| `defer_plan_continuation` | Pause an active plan for later resumption |
| `ask_clarification` | Ask the user a clarifying question with clickable options |
| `generate_image` | Generate an image via the configured image model |
| `list_session_attachments` | List all attachments in the current session |
| `view_session_attachment` | View contents of a specific session attachment |
| `send_files` | Send files or directories from the workspace to the user |

## Telegram commands

| Command | Description |
|---------|-------------|
| `/start` | Start a new session |
| `/new` | Start a new session (clears previous history) |
| `/status` | Show Ollama version, loaded models, and VRAM usage |
| `/settings` | View and change model roles via inline keyboard |
| `/sessions` | List the 10 most recent sessions with titles and IDs |
| `/session <id>` | Switch to a specific session |
| `/goal <objective>` | Set a persistent goal for autonomous execution |
| `/goal` | Check current goal status |
| `/goal pause` / `resume` / `clear` | Control goal execution |
| `/projects` | List active autonomous projects |
| `/memory <query>` | Search semantic memory |
| `/feedback <text>` | Submit feedback for the agent to learn from |
| `/reloadmodels` | Reload model inventory and save snapshot |

## Advanced features

- **Sleep mode** (`SLEEP_MODE_ENABLED`): when the user is inactive, the agent runs background learning cycles using the learning model to reflect on past conversations and consolidate memory. Includes GPU load checking via `/api/ps` to avoid slowing down the machine.

- **Goals** (`/goal`): set a persistent objective and the agent executes autonomous iterations (up to 20 cycles) with progress evaluation via subagents. Works in both Web and Telegram.

- **Autonomous projects**: background workspace tasks with a web dashboard. Telegram receives proactive notifications on task completion or failure.

- **Skills**: custom reusable instruction sets stored as markdown files with frontmatter. The agent can create, edit, and invoke skills. Duplicate prevention via name similarity (Levenshtein) and description similarity (Jaccard).

- **RAG memory**: long-term semantic memory using local embeddings. The agent autonomously manages what to store, search, and delete. Pre-fetched on each turn with a 0.70 similarity threshold. Consolidation prevents duplicates above 0.85 cosine similarity.

- **Plan system**: structured multi-step plans with user approval, per-step completion tracking, deferred continuation, and background resumption via `PlanMonitor`.

- **User feedback loop**: text feedback (corrections, preferences, praise) saved via Web UI or Telegram `/feedback`, processed during sleep mode learning cycles.

See [`docs/`](docs/) for detailed documentation on each feature.

## CLI commands

```bash
go run ./cmd/ollamabot probe models
go run ./cmd/ollamabot probe snapshot --out docs/probe-cache.json
go run ./cmd/ollamabot probe chat --model <name>
go run ./cmd/ollamabot probe tools --model <name>
go run ./cmd/ollamabot probe json --model <name>
go run ./cmd/ollamabot probe thinking --model <name>
go run ./cmd/ollamabot probe embeddings --model <name>
go run ./cmd/ollamabot probe vision --model <name> --image /path/to/image.jpg
go run ./cmd/ollamabot probe audio --model <name> --audio /path/to/audio.wav
go run ./cmd/ollamabot docs generate --out docs
go run ./cmd/ollamabot serve --port 8080 --cache docs/probe-cache.json
go run ./cmd/ollamabot version
```

Generated references: [`docs/ollama-reference.md`](docs/ollama-reference.md),
[`docs/local-model-inventory.md`](docs/local-model-inventory.md). See [`docs/README.md`](docs/README.md)
for progress notes and probe results.

## Web UI

- Streamed chat with thinking blocks, tool steps, and metrics
- Multimodal attachments (image, audio, files) with drag & drop
- Session sidebar with persisted history under `SESSIONS_PATH`
- Settings for Ollama URL, model roles, sleep mode, web search, Telegram, paths
- Memory explorer: search, add, delete, and re-index RAG entries
- Skills explorer: list, view, edit, and delete custom skills
- Project dashboard for autonomous tasks
- Real-time sync via SSE between Web and Telegram activity
- Optional password (`SERVER_PASSWORD`) and LAN exposure (`SERVER_EXPOSE_NETWORK`)

Runtime model selection is **not** stored in the browser; the UI reflects server config only.

**Chrome / Edge / Brave on LAN:** for microphone or clipboard on a non-HTTPS origin, enable
`chrome://flags/#unsafely-treat-insecure-origin-as-secure` and add your manager URL
(e.g. `http://192.168.1.50:8080`), then relaunch.

## Build

```bash
# Windows
bash build-win.sh

# macOS (Apple Silicon)
bash build-mac.sh

# Manual
go build -ldflags "-X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" ./cmd/ollamabot
```

## Project structure

| Path | Purpose |
|------|---------|
| `cmd/ollamabot/` | Process entry, shared managers, channel startup |
| `internal/engine/` | `ProcessTurn`, context injection, session naming |
| `internal/agent/` | Agent loop, goals, autonomous projects, plan monitor, XML fallback |
| `internal/router/` | Media routing (vision/audio pre-processing) |
| `internal/tools/` | Tool registry and handlers (20+ tools) |
| `internal/sessions/` | Session persistence, approvals, attachments |
| `internal/memory/` | RAG memory store with embeddings |
| `internal/learning/` | Sleep manager, feedback loop |
| `internal/skills/` | Skill catalog and management |
| `internal/web/` | Web server, SSE, static UI |
| `internal/telegram/` | Telegram bot, media download, inline keyboards |
| `internal/config/` | `.env` loading, model role resolution |
| `internal/probe/` | Model capability probing |
| `internal/ollama/` | HTTP client for Ollama REST API |
| `.env` / `.env.example` | Runtime configuration |

## Agent guidelines

See [`AGENTS.md`](AGENTS.md) for module layout, lifecycle, and contribution rules for coding agents.

## License

[MIT](LICENSE) © 2026 Jonathan Hecl
