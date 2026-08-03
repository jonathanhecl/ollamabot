# Progress

## 2026-08-03 — Automatic Session Context Pre-fetch, Smart VRAM Auto-Unload, Goal Progress Bar & Milestones

Implemented 3 high-impact intelligence, VRAM management, and goal tracking capabilities (`internal/agent`, `internal/ollama`, `internal/sessions`, `internal/learning`):

### 1. Automatic Past Session Context Pre-fetch (`internal/agent/loop.go`)
- **Proactive Context Pre-fetch**: Automatically evaluates user prompts during `agent.Run` against past session titles and goal objectives stored in `SessionStore`.
- Injects a `## Automatically Recalled Context from Relevant Past Sessions` system prompt block so local LLMs immediately know relevant past chat context without requiring explicit tool calls.

### 2. Smart VRAM Auto-Unload Manager (`internal/ollama/client.go`, `internal/learning/sleep_manager.go`)
- **VRAM Reclaim (`keep_alive: 0`)**: Added `UnloadModel` and `UnloadInactiveModels` methods to `ollama.Client` to force GPU VRAM release.
- **Sleep Manager Release**: Automatically unloads non-default/secondary models (vision, audio, subagents) when sleep mode pauses/resumes, keeping VRAM free for the user's GPU.

### 3. Goal Milestones & Progress Bar Rendering (`internal/sessions/sessions.go`, `internal/agent/goal.go`)
- **Goal Structure Enhancements**: Added `GoalMilestone` struct (`Description`, `Completed`, `CompletedAt`) and `GoalProgress` percentage field to `sessions.Session`.
- **Visual Progress Bar**: Added `RenderProgressBar(percent int)` helper returning ASCII progress bars (e.g. `[██████░░░░] 60%`) formatted for Telegram messages and Web UI API.

### 2. Executive Digest & Session Export Tools (`internal/tools/tools.go`, `internal/agent/loop.go`)
- **`sessions_digest`**: Read-only tool that retrieves structured daily digests or builds an executive summary of activity across past sessions over a specified timeframe (`since_days` or date range).
- **`session_export`**: Tool that exports any session transcript into a clean, formatted Markdown document (`exports/session_<id>.md`) in the workspace for documentation or sharing.

### 3. Tests & System Integration (`internal/tools/sessions_tools_test.go`)
- Unit tests added for `sessions_digest` and `session_export`.
- All tests (`go test ./...`) passing cleanly.

### 2. Multi-word Tokenized Search & Length Safeguards (`internal/tools/tools.go`)
- **Tokenized Search**: `sessions_search` splits queries into words/tokens and matches messages containing all search terms even if non-contiguous.
- **Context Limit Protection**: `session_read` caps output string length at ~12,000 characters (~3k tokens), truncating older turns with a `[SYSTEM NOTE]` when transcripts exceed limits.

### 3. Conditional System Prompt & Tests (`internal/agent/loop.go`, `internal/tools/sessions_tools_test.go`)
- System prompt instruction in `loop.go` is now conditionally injected only when `sessionStore` is configured.
- Comprehensive unit tests covering `date_from`/`date_to` range filtering, untitled session previews, tokenized multi-word search, and transcript truncation.

## 2026-08-03 — MCP tool optimization: origin enrichment, active server context, schema fallback, empty result handling

Four key improvements to MCP tool handling and local model routing (`internal/tools`, `internal/mcp`, `internal/agent`):

### 1. Tool Description Origin Enrichment (`internal/tools/tools.go`)
- Prefix tool descriptions sent to Ollama with `[MCP Server: <server_name>] <description>` in both `SetMCPManager` and `RefreshMCP`.
- Gives local models explicit context on which MCP server host and dataset each tool operates on.

### 2. Input Schema Sanitization (`internal/tools/tools.go`)
- Default `InputSchema.Type` to `"object"` if empty or omitted by an MCP server.
- Prevents invalid function schemas (`"type": ""`) from confusing Ollama tool parsers.

### 3. Dynamic Active Server Context in System Prompt (`internal/agent/loop.go`)
- System prompt now queries `a.registry.MCPManager().GetServersStatus()` and dynamically lists connected MCP servers with their status and available tool names.
- Gives the local model an explicit map of online MCP services at turn initialization without requiring a prior `mcp_list_servers` call.

### 4. Empty Result Handling & Proactive Guidance (`internal/mcp/manager.go`, `internal/agent/loop.go`)
- `mcp.Manager.Execute` now returns `"[MCP Tool executed successfully, but returned empty output or no results.]"` when a tool completes with empty text output.
- `postProcess` appends proactive system guidance when an MCP tool returns 0 results, nudging local models to broaden search terms or check server status rather than retrying identical parameters or falling back to workspace tools.

## 2026-07-30 — Autonomous harness: staleness recovery, post-task verification, callback cleanup

Three robustness improvements to the autonomous project manager
(`internal/agent/autonomous.go`):

### 1. Stale task recovery on startup

If the process dies while a task is `in_progress`, the in-memory `isWorking`
flag is lost and the task stays stuck forever. `RecoverStaleTasks()` now runs
at the start of the heartbeat loop: it scans all `in_progress` projects and
resets any task whose `UpdatedAt` is older than `AUTONOMOUS_STALE_TASK_MINUTES`
(default 30) back to `pending`, recording the previous result for context.

### 2. Post-execution verification

After each task's `agent.Run` completes, `verifyTask()` runs a second, focused
agent turn (using the subagent model when available) that independently
inspects the project workspace with `list_files`/`read_file` and returns
structured JSON (`success`, `evidence`, `gaps`). If verification fails, the
task is marked `failed` with the identified gaps instead of `completed`,
preventing the agent from "finishing" projects that don't actually work.
Controlled by `AUTONOMOUS_VERIFICATION_ENABLED` (default true). Verification
errors fall back to accepting the task, so verification issues never block
otherwise-complete tasks.

### 3. `OnTaskCompletion` moved from global to struct field

The package-level `var OnTaskCompletion` was replaced by a field on
`AutonomousManager` with a `SetOnTaskCompletion()` setter and
`notifyTaskCompletion()` helper. This avoids global state collisions between
multiple manager instances (tests, multi-tenant). `telegram/bot.go` updated to
use the new setter.

### Config additions (`internal/config/config.go`, `.env.example`)

- `AUTONOMOUS_STALE_TASK_MINUTES` (default 30): staleness threshold in minutes.
- `AUTONOMOUS_VERIFICATION_ENABLED` (default true): toggle post-task verification.

### Tests (`internal/agent/autonomous_test.go`)

- `TestAutonomousManager_RecoverStaleTasks`: stale task reset, fresh task
  preserved, completed project untouched.
- `TestAutonomousManager_RecoverStaleTasks_DefaultThreshold`: verifies the
  30-minute default when the config value is 0.
- `TestAutonomousManager_SetOnTaskCompletion`: callback invocation and nil safety.
- `TestVerificationResponse_Parsing`: JSON schema parsing including markdown
  fence stripping and invalid JSON handling.

## 2026-07-30 — Agent harness efficiency: system prefix caching, parallel tools, autonomous context

Three improvements to make the autonomous agent more efficient:

### 1. System prefix caching (`internal/agent/loop.go`)

Previously, every loop iteration (up to 50) re-read `SOUL.md`, `USER_PROFILE.md`,
skills, and rebuilt 8+ static system messages from disk. Now these are computed
**once** before the loop in `buildStaticSystemPrefix()` and reused every
iteration. Only the date/time, todo progress, and plan reinforcement (which
actually change) are rebuilt per iteration as `dynamicPrefix`.

### 2. Parallel tool execution (`internal/agent/loop.go`)

When the model returns multiple `tool_calls` in a single response, read-only
tools (`fetch_webpage`, `web_search`, `read_file`, `list_files`, `search_files`,
`memory_search`, `memory_list`, `mcp_list_servers`, MCP read/list/get tools)
are now executed **concurrently** with goroutines. Stateful tools
(`write_file`, `edit_file`, `execute_command`, `complete_plan_step`, etc.)
remain sequential. If any call in the batch is not parallel-safe, the entire
batch runs sequentially to preserve state ordering (e.g. `planStepHasAction`
must be visible to `complete_plan_step`).

New helper: `isParallelSafeTool(toolName, params, toolSource)`.

### 3. Autonomous prior task context (`internal/agent/autonomous.go`)

`ExecuteTask` now injects a "## Prior Task Context" section into the system
prompt with the content and results of previously completed tasks in the same
project (truncated to 500 chars per task). This prevents the agent from
re-reading files or re-fetching web pages that prior tasks already gathered.

### Tests

- `TestAgentRunParallelToolExecution`: 3 `read_file` calls execute without
  deadlock.
- `TestAgentRunSequentialForStatefulTools`: mixed `read_file` + `write_file`
  batch runs sequentially; `write_file` creates the file.
- `TestAutonomousPriorTaskContext`: `ExecuteTask` injects prior task context
  into system messages.

## 2026-07-29 — Agent loop: global cap for network tools (fetch_webpage, web_search)

The per-signature loop detector only counted identical calls (same tool + same
normalized args). The model could cycle through many DIFFERENT URLs/queries —
each under the threshold — and fetch for hours without triggering any detector.
This was observed in a real session: ~40 `fetch_webpage` calls across ~15
different URLs over 2 hours before any single URL hit the 5-call threshold.

### Changes

- `internal/agent/loop.go`:
  - **Per-tool global counter** (`toolGlobalCounts`): tracks total calls per
    tool name across all argument variants in a single `Run`.
  - **Network tools** (`fetch_webpage`, `web_search`) get:
    - Per-signature threshold lowered from 5 to **3** (same URL/query 3 times
      → abort).
    - Global cap: warn at **8** total calls, abort at **12** total calls in
      one turn, regardless of URL/query differences. The abort message tells
      the model to synthesize an answer from data already fetched.
  - New helper: `isNetworkFetchTool`.
- `internal/agent/plan_loop_test.go`: new test
  `TestAgentRunStopsExcessiveNetworkFetch` verifying that cycling through 13
  different URLs (each called once) is caught by the global cap.

## 2026-07-29 — Agent loop: stop MCP/workspace confusion and tighter loop detection

The agent was getting stuck calling `list_files` repeatedly after `vault_list`
returned filenames like `OpenAI.md`. Root cause: the model saw MCP-returned
filenames and tried to read them with workspace tools (`read_file`,
`list_files`), which failed because those files live inside the MCP service
(Obsidian vault), not in the local workspace. The loop detector then aborted
after 5 wasted iterations with a misleading hint that pushed the model toward
*more* file operations.

### Changes

- `internal/agent/loop.go`:
  - **MCP system prompt** rewritten to explicitly state that files returned by
    MCP tools live inside the external service, not the workspace, and that the
    model must use the matching MCP read/list/write tool — never `read_file`,
    `list_files`, `write_file`, `edit_file`, or `apply_diff` — on them.
  - **Loop detection** is now tool-aware:
    - No-op calls (e.g. `list_files {}`, `vault_list {}`) abort after **3**
      iterations instead of 5, since repeating them never produces new data.
    - Meaningful calls still abort at 5.
    - The warning hint is now tool-specific. The old generic hint ("verify the
      file using read_file") was harmful for non-read tools and has been
      replaced with targeted advice (use MCP tools, change args, ask user).
  - **New guardrail**: filenames returned by MCP tools during a turn are
    tracked. When a workspace file tool fails on a path whose basename matches
    an MCP-returned filename, a warning is appended telling the model to use
    the MCP tool instead. This catches the exact `vault_list` → `read_file`
    confusion pattern.
  - New helpers: `isNoOpToolCall`, `repetitiveLoopHint`,
    `isWorkspaceFileTool`, `collectMCPReturnedNames`, `hasFileExtension`,
    `isCommonNonFileToken`, `matchMCPReturnedName`.
- `internal/agent/plan_loop_test.go`: new test
  `TestAgentRunStopsNoOpLoopEarly` verifying that `list_files` with empty args
  aborts after 3 calls (vs 5 for meaningful calls).

## 2026-07-29 — MCP tool cache: tools stay visible when a server is down

If an MCP server was unreachable at startup, `tools/list` failed and its
tools were never registered, so the model could not see them and fell back to
workspace file writes instead of using the MCP. Now the last successful tool
list is cached and re-registered on failure, with a lazy reconnect on use.

### Changes

- `internal/mcp/manager.go`: `startServerNoLock` persists each successful
  `tools/list` to `mcp_tools_cache.json` (next to `mcp_config.json`). When a
  server fails to start, its cached tools are registered and the server is
  marked `degraded` (visible in `GetServersStatus` as `degraded: true` with
  the original error). `Execute` attempts a lazy reconnect
  (`reconnectServer`) using the last known config before failing with a clear
  "server unreachable" error that instructs the model to inform the user.
  `DeleteServer` also removes the server's cache entry.
- `internal/mcp/mcp_test.go`: new tests `TestManagerDegradedCache` (cached
  tools registered for a down server, degraded status, unreachable error on
  Execute) and `TestManagerToolsCacheWritten` (cache persisted after a
  successful tools/list).

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

## 2026-08-03 — Telegram MCP tool source labels alignment with Web UI

Updated Telegram channel tool execution and approval prompts to display the exact MCP server source (e.g. `(mcp:obsidian)`) matching the Web interface instead of generic `(MCP)`.

- `internal/telegram/bot.go`: `OnToolStart` formats tool execution messages with the full `source` label (e.g. `vault_list (mcp:obsidian)`). `RequestApproval` includes `(mcp:<server>)` in the confirmation dialog.
- `internal/telegram/bot_test.go`: added unit tests for MCP tool label formatting with source labels.

## 2026-08-03 — Fix MCP tool execution with empty arguments (`arguments: {}`)

Fixed a bug in MCP JSON-RPC payload generation where `CallToolParams.Arguments` had `json:"arguments,omitempty"`. When calling tools with empty arguments `{}` (such as `vault_list`), Go's JSON encoder omitted the `arguments` key entirely (`{"name": "vault_list"}`), causing strict MCP SDKs (e.g. Node/Zod validators) to reject calls with `Input validation error: Invalid arguments ... received undefined`.

- `internal/mcp/types.go`: removed `omitempty` tag from `CallToolParams.Arguments`.
- `internal/mcp/manager.go`: ensured `args` is initialized to non-nil `map[string]any` before building `CallToolParams` so empty arguments strictly output `"arguments": {}`.
- `internal/mcp/mcp_test.go`: added unit test `TestCallToolParams_EmptyArgumentsJSON`.
- `internal/agent/loop.go`: added early system warning on `repeatCount >= 2` for no-op/list calls and enhanced `repetitiveLoopHint` for MCP list tools to explicitly direct the model to read individual files with MCP read/get tools.

## 2026-08-03 — Model sorting and metadata controls in Web UI

Added flexible model sorting and additional metadata (disk size, installation date) to the "Manage Models & Roles" modal in the Web UI.

- `internal/capabilities/capabilities.go`: exposed `Size` (bytes) from `ollama.ModelTag` in `ModelReport`.
- `internal/web/server.go`: added `ModifiedAt` and populated `Size` & `ModifiedAt` in `ModelView` response.
- `internal/web/static/index.html`: added `<select id="modelSort">` dropdown with 11 sorting modes (Parameter Size, File/Disk Size, Context Length, Installation Date, Assigned Roles, Name).
- `internal/web/static/app.js`: implemented `parseParamCount`, `getActiveRolesCount`, `isRoleActiveForCap`, and `formatDateShort`. Updated `capBadges` to hide missing capabilities completely, show supported capabilities with neutral styling by default, and color tags only when actively assigned to a role. Fixed sorting by assigned roles so all models with assigned roles (Main, Learn, Subagent, Vision, Audio, Embed, Image) are neatly grouped at the top ordered by active role count. Removed duplicate disk label in `model-meta-info`.
- `internal/web/static/styles.css`: added `.cap.inactive-cap` style for uncolored, neutral capability tags. Fixed `.model-meta-info` flex-direction layout to horizontal row so metadata stays clean and aligned on a single line.
