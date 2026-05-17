# ollamabot

OllamaBot is a modular Go console for local Ollama models. By default, run it
without parameters: it loads `.env`, creates one interactively if missing, and
starts the local web UI when enabled.

## Configuration

Copy `.env.example` to `.env` and adjust as needed:

```env
OLLAMA_BASE_URL=http://localhost:11434
WEB_ENABLED=true
WEB_ADDR=:8080
OLLAMA_PROBE_MODELS=
OLLAMA_DEFAULT_MODEL=
TELEGRAM_BOT_TOKEN=
```

`TELEGRAM_BOT_TOKEN` and `WEB_ADDR` are reserved for the next phase.

Normal use:

```powershell
go run ./cmd/ollamabot
```

If `.env` does not exist, the app asks for Ollama URL, whether to start the web
server, and the web port.

## Commands

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

Generated references live in `docs/ollama-reference.md` and
`docs/local-model-inventory.md`.

See `docs/README.md` for the project progress log, pending work, Ollama usage
notes, and local probe results.

The web UI supports runtime Ollama URL configuration, model selection from a
modal, streamed chat responses, thinking blocks, multimodal file/paste inputs,
and visible future tool-call events.
