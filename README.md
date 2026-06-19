# ollamabot

Local-first autonomous agent powered by [Ollama](https://ollama.com). One Go process runs the
agent core; **Web** and **Telegram** are transport channels that talk to the same engine. Chat
behavior, model choice, tools, sessions, and background jobs do not depend on which channel you use.

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

If `.env` is missing, the binary runs an interactive setup (Ollama URL, web server, port).

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

Capabilities are probed via `internal/probe`; missing roles disable features gracefully without user-facing errors.

## CLI commands

```bash
go run ./cmd/ollamabot probe models
go run ./cmd/ollamabot probe snapshot --out docs/probe-cache.json
go run ./cmd/ollamabot probe chat --model <name>
go run ./cmd/ollamabot probe tools --model <name>
go run ./cmd/ollamabot probe vision --model <name> --image /path/to/image.jpg
go run ./cmd/ollamabot probe audio --model <name> --audio /path/to/audio.wav
go run ./cmd/ollamabot docs generate --out docs
go run ./cmd/ollamabot serve --port 8080 --cache docs/probe-cache.json
```

Generated references: [`docs/ollama-reference.md`](docs/ollama-reference.md),
[`docs/local-model-inventory.md`](docs/local-model-inventory.md). See [`docs/README.md`](docs/README.md)
for progress notes and probe results.

## Web UI

- Streamed chat with thinking blocks, tool steps, and metrics
- Multimodal attachments (image, audio, files)
- Session sidebar with persisted history under `SESSIONS_PATH`
- Settings for Ollama URL, model roles, sleep mode, web search, Telegram, paths
- Optional password (`SERVER_PASSWORD`) and LAN exposure (`SERVER_EXPOSE_NETWORK`)

Runtime model selection is **not** stored in the browser; the UI reflects server config only.

**Chrome / Edge / Brave on LAN:** for microphone or clipboard on a non-HTTPS origin, enable
`chrome://flags/#unsafely-treat-insecure-origin-as-secure` and add your manager URL
(e.g. `http://192.168.1.50:8080`), then relaunch.

## Agent guidelines

See [`AGENTS.md`](AGENTS.md) for module layout, lifecycle, and contribution rules for coding agents.
