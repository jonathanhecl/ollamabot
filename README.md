# ollamabot

OllamaBot is starting as a modular Go probe layer for local Ollama models.
This first phase loads `.env`, talks to Ollama's REST API, inventories local
models, detects reported/inferred capabilities, and generates reference docs.

## Configuration

Copy `.env.example` to `.env` and adjust as needed:

```env
OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_PROBE_MODELS=
OLLAMA_DEFAULT_MODEL=
TELEGRAM_BOT_TOKEN=
WEB_ADDR=:8080
```

`TELEGRAM_BOT_TOKEN` and `WEB_ADDR` are reserved for the next phase.

## Commands

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

Generated references live in `docs/ollama-reference.md` and
`docs/local-model-inventory.md`.

See `docs/README.md` for the project progress log, pending work, Ollama usage
notes, and local probe results.
