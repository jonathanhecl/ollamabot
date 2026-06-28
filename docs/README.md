# OllamaBot Docs

This folder holds the living state of the project: what's done, what's pending, Ollama reference, and test results from the local instance.

## Documents

- [progress.md](progress.md): development log, decisions made, and next steps.
- [ollama-usage.md](ollama-usage.md): practical guide to using the Ollama API in this project.
- [probe-results.md](probe-results.md): manual probe runs and observed results.
- [probe-cache.json](probe-cache.json): cached snapshot of expected results/models to avoid re-probing every time.
- [ollama-reference.md](ollama-reference.md): generated reference with minimal payloads and source links.
- [local-model-inventory.md](local-model-inventory.md): inventory generated from `/api/tags` and `/api/show`.
- [media-routing.md](media-routing.md): full media routing flow for audio/image attachments, including the decision matrix and SSE event format.
- [walkthrough.md](walkthrough.md): walkthrough of Telegram proactive notifications and frontend integration tests (Playwright).

## Current Status

The system is fully implemented in Go and can:

- Run without parameters as the normal flow.
- Create `.env` interactively if missing.
- Read configuration from `.env`.
- Connect to Ollama via REST.
- List installed models and query metadata/capabilities via `/api/show`.
- Run probes for chat, tools, structured JSON, vision, thinking, embeddings, and experimental audio.
- Generate local documentation.
- Save a cached snapshot of models/expected results.
- Serve a local web UI for browsing models, capabilities, memory, sessions, skills, and chatting with a main model.
- Run a Telegram bot with full feature parity: chat, media, inline keyboards, session management, goals, projects, and feedback.
- Execute an autonomous agent loop with 20+ tools, plan management, goals, and background projects.
- Perform multimodal routing (vision/audio) with structured transcription and dedicated model roles.
- Manage long-term RAG memory with local embeddings.
- Run sleep-mode learning cycles for background reflection and memory consolidation.
