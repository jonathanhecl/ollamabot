# Progress

## 2026-07-29 — MCP hardening: SSE lifetime, targeted restarts, secret masking

Follow-up fixes after reviewing the multi-transport MCP work.

### Changes

- `internal/mcp/http_transport.go`: the legacy SSE transport now uses a
  dedicated `streamClient` without `http.Client.Timeout` for the long-lived
  SSE GET (the 60s client timeout was silently killing the stream; the
  timeout remains for POSTs).
- `internal/mcp/client.go`: new `failPending` helper drains in-flight
  requests with a JSON-RPC error when the connection dies; both the SSE and
  stdio `readLoop`s call it on exit so callers unblock immediately instead of
  hanging until context timeout. The `transport` interface now declares
  `protocolVersion()`: stdio/SSE announce `2024-11-05`, Streamable HTTP
  announces `2025-03-26`.
- `internal/mcp/manager.go`: `AddOrUpdateServer` and `DeleteServer` now
  restart only the affected server (`startServerNoLock`/`stopServerNoLock`)
  instead of restarting every configured server.
- `internal/tools/tools.go`: `mcp_list_servers` masks `env` and `headers`
  values before returning them, since the output goes into the model's
  context; the authenticated web API still serves raw values.
- `internal/web/static/app.js`: content-step fallbacks — legacy sessions
  without `content` steps render their text after leading thinking steps, and
  stream errors / user aborts are appended as `content` steps so they remain
  visible in the chronological timeline.

## 2026-07-29 — Chronological action steps in session and web UI

The recorder and web UI treated the assistant's final `content` as a separate
block, with all `thinking` steps forced before it and all tool/plan steps forced
after it. For an agent that runs multiple action blocks per turn, this lost
the real order. Now `content` is stored as a proper step and the UI renders
`Steps` in exact chronological order.

### Changes

- `internal/sessions/recorder.go`: added `AppendContentStep` and made `OnContent`
  append/merge a `content` step into `currentTurn.Steps` while still keeping
  `msg.Content` as the canonical final text.
- `internal/web/static/app.js`: the `content` SSE handler now mirrors the
  `content` step; `renderStep` gained a `content` case; `buildAssistantMessageHTML`
  renders `steps` in array order and only falls back to legacy
  `content`/`thinking`/`toolCalls` for older messages without `steps`.
- `internal/sessions/recorder_test.go`: updated
  `TestRecorderSnapshotsMultipleAssistantTurns` and
  `TestMergeFinalHistoryPreservesBaseAssistantSteps` to expect the new `content`
  step; added `TestAppendContentStep`.

### Notes

- `msg.Content` is kept unchanged so Ollama chat history and Telegram continue
  to work.
- The streaming cursor now appears at the last live step, whether it is
  `content` or `thinking`.

## 2026-07-29 — MCP remote transports (http + sse)

The MCP client previously supported only the `stdio` transport (launching a
local subprocess and exchanging JSON-RPC over stdin/stdout). When users
provided a remote server config such as the Obsidian Local REST API
(`"type": "http", "url": "https://127.0.0.1:27124/mcp/"`), the agent had no
way to express it through the `mcp_add_server` tool (whose schema required
`command`), so it silently translated the request into a stdio invocation
(`uvx mcp-obsidian` + env vars) that did not match the user's intent and never
connected.

### Changes

- `internal/mcp/client.go`: refactored `Client` onto a `transport` interface.
  `NewClient` now takes a `ServerConfig` and selects the transport by
  `cfg.Type`.
- `internal/mcp/stdio.go`: stdio transport (moved from the old `client.go`).
- `internal/mcp/http_transport.go`: new `http` (Streamable HTTP, MCP
  2025-03-26) and `sse` (legacy HTTP+SSE, MCP 2024-11-05) transports. The
  http transport POSTs JSON-RPC to `cfg.URL` and accepts either a single
  JSON document or an SSE stream in the response. The sse transport opens a
  long-lived SSE stream, learns the POST endpoint from the server's
  `endpoint` event, and routes responses by id. Both honor `cfg.Headers`
  (e.g. `Authorization: Bearer ...`).
- `internal/mcp/manager.go`: `ServerConfig` and `MCPServerStatus` gained
  `Type`, `URL`, and `Headers` fields. `startNoLock` uses the new
  `NewClient(name, cfg)` signature and logs the transport.
- `internal/tools/tools.go`: the `mcp_add_server` tool schema now accepts
  `type` (stdio|http|sse), `url`, and `headers`; `command` is only required
  for stdio. The handler validates per-transport requirements and builds the
  full `ServerConfig`. The risk-assessment summary reflects the transport.
- `internal/web/server.go`: `updateMCPServerRequest` carries the new fields.
- `internal/web/static/index.html` + `app.js`: the MCP edit dialog exposes a
  transport selector and conditionally shows stdio (command/args/env) or
  remote (url/headers) fields. The server list shows the transport and URL
  for remote servers.
- `internal/mcp/mcp_test.go`: updated for the new `NewClient` signature.

### Notes

- The shared HTTP client allows proxies via `http.ProxyFromEnvironment` and
  uses a 60s timeout. Local-first servers (e.g. Obsidian on 127.0.0.1) with
  self-signed certs work because Go's default TLS config is used; if a remote
  server presents an untrusted cert, set `SSL_CERT_FILE` / `SSL_CERT_DIR` in
  the environment.
- Existing stdio configs (no `type` field) continue to work unchanged.

## 2026-07-29 — MCP remote transport hardening

Follow-up to the http/sse transport support. Users reported that the Obsidian
Local REST API connection still failed and the UI showed a generic 500. Made
saving an MCP server fail-fast and informative:

- `internal/mcp/manager.go`: `AddOrUpdateServer` now validates the server
  (initialize + `tools/list`) *before* writing the config. If the server cannot
  be reached or cannot list tools, the config is not persisted and a typed
  `ValidationError` is returned. `startNoLock` records per-server start errors
  in `statusErrors` and only registers tools once `tools/list` succeeds.
- `internal/web/server.go`: `handleUpdateMCPServer` distinguishes
  `*mcp.ValidationError` and returns `400 Bad Request` with the concrete
  message, instead of 500.
- `internal/mcp/http_transport.go`: `InsecureTLS` is now wired into a per-
  transport `tls.Config` so `insecure_tls` actually skips certificate
  verification for local HTTPS servers.
- `internal/mcp/manager.go`, `tools.go`, `web/server.go`, `web/static/index.html`
  + `app.js`: exposed `insecure_tls` field, fixed checkbox layout, and improved
  error display in the server list and save toast.
- `internal/web/static/app.js`: fetch error handler now falls back to the raw
  `statusText` if the server response cannot be parsed as JSON, and surfaces the
  backend `error` field when present.
- `internal/mcp/client.go` + `http_transport.go`: `notifications/initialized`
  now sends an empty `params` object and the HTTP transport always advertises
  `Accept: application/json, text/event-stream` on POSTs. Some servers
  (Obsidian Local REST API) reject notifications sent with `Accept: application/json`
  only and expect the params field to be present.
  Confirmed working against the Obsidian Local REST API MCP server via HTTPS
  with `insecure_tls` enabled.

## 2026-07-29 — MCP tool source labels in UI

To make it clear when an executing tool comes from an MCP server, the tool
execution path now carries a `source` label (e.g. `mcp:obsidian`) from the
registry through the recorder to the web UI.

- `internal/mcp/manager.go`: added `GetToolServer` to look up the server name for
  a registered MCP tool.
- `internal/tools/tools.go`: added `GetToolSource` returning `mcp:<server>` for
  MCP tools and `internal` for built-ins.
- `internal/agent/loop.go`: `StreamHandler` `OnToolStart`/`OnToolResult` now
  receive `source`; `agent.loop` resolves it via `registry.GetToolSource`.
- `internal/web/server.go`, `internal/telegram/bot.go`, `internal/engine/engine.go`,
  `internal/agent/goal.go`, `internal/agent/autonomous.go`,
  `internal/learning/sleep_manager.go`: updated all `StreamHandler` implementors.
- `internal/sessions/sessions.go` + `recorder.go`: `Step` now has a `Source`
  field that is persisted and returned with session messages.
- `internal/web/static/app.js`: `tool_start`/`tool_result` SSE events carry
  `source`; `renderStep` appends ` (mcp:obsidian)` next to the tool name so the
  user sees where each tool is running.
- All tests updated for the new `OnToolStart`/`OnToolResult` signatures; full
  suite passes.

## 2026-07-29 — Tool descriptions for direct URL fetching

When the user provides a URL (e.g. an X post) and asks to save it, the model
was defaulting to `web_search` instead of the existing `fetch_webpage` tool.
Adjusted tool descriptions in `internal/tools/tools.go` to steer the model:

- `web_search`: now explicitly says not to search for a URL the user already
  provided and to prefer `fetch_webpage` for URLs.
- `fetch_webpage`: now explicitly mentions it should be used when the user
  supplies a URL to analyze, summarize, or save.
