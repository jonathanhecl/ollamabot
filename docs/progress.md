# Progress

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
