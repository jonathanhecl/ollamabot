# Agents

## Agent Guidelines

### Objective

Modular autonomous agent based on local Ollama models. Detects installed models, evaluates their capabilities via probes, and uses them accordingly. Intended channels are Telegram and a local web UI for quick testing.

### Design Principles

- **Modular**: each capability (chat, vision, audio, tools, embeddings) is a swappable module. The agent does not assume a single model covers everything.
- **Local-first**: all models run on local Ollama. No external LLM dependencies.
- **Graceful fallback**: if a capability is unavailable (no model supports it), that feature is silently disabled — no errors surfaced to the user.
- **No mutable global state**: conversation state lives in the channel (Telegram chat ID, web session), not in the agent.

### Model Router

The agent uses a router to assign the correct model based on input type:

| Role | When used | Fallback |
|---|---|---|
| `main` | plain text, history, final response | — (required, needs `completion` + `tools`) |
| `vision` | image attachments | main if it has vision, otherwise feature is unavailable |
| `audio` | audio attachments | main if it has audio, otherwise feature is unavailable |
| `embeddings` | future semantic search | none for now |

When a dedicated role model different from main is configured for vision or audio, the flow is:
1. The role model analyzes the object and produces a detailed textual description.
2. That description is injected as context into the message sent to main.
3. Main drafts the final response using the conversation history and the user's prompt.

### Internal Tools

- Tools are Go functions registered in the agent, not in the model.
- The model decides to call a tool via `tool_calls`; the agent executes it and returns the result automatically.
- Each tool must declare: name, description, parameters (JSON Schema), and a Go handler.
- Security: per-channel tool allowlist, timeouts, and audit log for every invocation.
- Planned tools: web search, file reading, safe command execution, calendar lookup.

### Memory and Conversations

- Message history is maintained per channel (Telegram chat ID or web session).
- Each message includes role (`user`, `assistant`, `tool`), content, and timestamp.
- The context window is trimmed if it exceeds the active model's `context_length`.
- Long-term memory: pending, via embeddings + semantic search.

### Channels

- **Web**: quick testing interface, no authentication, local only.
- **Telegram**: primary production channel. One bot per instance. `chat_id` identifies the session.

### Agent Lifecycle

```
input (user)
  → router determines active models
  → media pre-processing if applicable (vision/audio model)
  → context construction (history + media analysis)
  → main model call (streaming)
  → if tool_calls → execute tools → return result to model
  → final response to user
```

### Implementation Rules

- Each new module goes in `internal/<name>`.
- Do not add external dependencies without explicit justification.
- Every new capability must have a probe in `internal/probe` before being used in production.
- Changes to `.env` are persisted via `config.SaveBasic`.
- Progress is recorded in `docs/progress.md`.