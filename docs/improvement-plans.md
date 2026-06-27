# Improvement Plans — Autonomous Agent

Six areas of improvement identified during the agent implementation review. Each section includes the problem, impact, affected files, implementation steps, priority, and estimated effort.

---

## 1. Coordination Between Managers

**Priority: High | Effort: S**

### Problem

`GoalManager`, `PlanMonitor`, and `AutonomousManager` operate independently. `sessions.MarkProcessing` (`@/internal/sessions/processing.go:26`) is a reference-counting tracker, not a mutex. `PlanMonitor.Tick` (`@/internal/agent/plan_monitor.go:153`) checks for stalled plans and calls `MarkProcessing`, but `GoalManager.runGoalLoop` (`@/internal/agent/goal.go:268`) also calls `MarkProcessing` on the same session. Both can run concurrently on the same session ID.

### Impact

Two agent loops can execute simultaneously on the same session, causing:
- Duplicate tool executions and conflicting state mutations.
- Race conditions on `session.json` writes.
- Corrupted conversation history.

### Files involved

- `internal/sessions/processing.go` — `MarkProcessing`, `IsProcessing`
- `internal/agent/plan_monitor.go` — `Tick`, `resumePlan`
- `internal/agent/goal.go` — `runGoalLoop`
- `internal/agent/autonomous.go` — `Tick`, `ExecuteTask`

### Implementation plan

1. Add a `TryMarkProcessing(sessionID string) bool` function in `internal/sessions/processing.go` that returns `false` if `IsProcessing(sessionID)` is already true. This is an atomic check-and-mark under the existing `processingTrackerMu` lock.
2. Replace `MarkProcessing` calls in `PlanMonitor.resumePlan` (`:213`) with `TryMarkProcessing`. If it returns false, log and skip.
3. Replace `MarkProcessing` calls in `GoalManager.runGoalLoop` (`:268`) with `TryMarkProcessing`. If it returns false, log and return.
4. `AutonomousManager.ExecuteTask` operates on project workspaces, not sessions, so it does not need this check. No change required there.
5. Keep `MarkProcessing` in `engine.ProcessTurn` (`@/internal/engine/engine.go:80`) unchanged — user-initiated turns should always proceed.
6. Add a unit test in `internal/sessions/processing_test.go` for `TryMarkProcessing` covering: first call succeeds, second call on same ID fails, after `MarkIdle` it succeeds again.

---

## 2. Resource Limits for Subagents

**Priority: Medium | Effort: M**

### Problem

Subagents are instantiated via `agent.NewAgent` in multiple places (autonomous tasks, sleep reflector, goal loops, plan monitor). They share the same `ollama.Client` and have no per-invocation timeout. `MaxIterations = 50` (`@/internal/agent/loop.go:21`) caps the tool loop, but a single Ollama call can hang indefinitely if the model is overloaded or stuck.

### Impact

- A stuck subagent blocks its goroutine and consumes a model slot in Ollama.
- Multiple stuck subagents can exhaust VRAM and block the main agent.
- No way to recover without killing the process.

### Files involved

- `internal/agent/loop.go` — `Run` method, `MaxIterations`
- `internal/agent/autonomous.go` — `ExecuteTask` (`:430`)
- `internal/learning/sleep_manager.go` — `runLearningCycleForSessionsWithModel` (`:440`)
- `internal/agent/goal.go` — `runGoalLoop` (`:266`)
- `internal/agent/plan_monitor.go` — `resumePlan` (`:210`)
- `internal/config/config.go` — `Config` struct

### Implementation plan

1. Add `SubagentTimeoutMinutes int` to `Config` (`@/internal/config/config.go`), default 10. Add to `.env.example` as `SUBAGENT_TIMEOUT_MINUTES=10`.
2. Add a helper `func subagentContext(ctx context.Context, cfg config.Config) (context.Context, context.CancelFunc)` in `internal/agent/loop.go` that wraps `ctx` with `context.WithTimeout` using `cfg.SubagentTimeoutMinutes`. If the value is 0 or unset, use default 10 minutes.
3. In `ExecuteTask` (`@/internal/agent/autonomous.go:430`), wrap the `agent.Run` call with `subagentContext`. On `context.DeadlineExceeded`, log and mark the task as `failed` with result "Task timed out".
4. In `runLearningCycleForSessionsWithModel` (`@/internal/learning/sleep_manager.go:440`), wrap the `reflectorAgent.Run` call with `subagentContext`.
5. In `runGoalLoop` (`@/internal/agent/goal.go:266`), wrap each cycle's `agent.Run` call with `subagentContext` (not the entire loop — each cycle gets its own timeout).
6. In `resumePlan` (`@/internal/agent/plan_monitor.go:210`), wrap the `agent.Run` call with `subagentContext`.
7. Add a test in `internal/agent/loop_test.go` (or similar) verifying that `subagentContext` returns a context that expires after the configured duration.

---

## 3. Probe Learning Model Before Sleep Cycle

**Priority: Medium | Effort: S**

### Problem

`SleepManager.runLearningCycleForSessionsWithModel` (`@/internal/learning/sleep_manager.go:440`) instantiates a `reflectorAgent` with tools, but if `OllamaModelLearning` (or its fallback to `OllamaDefaultModel`) does not support tool calls, the agent will loop without executing any tools and eventually hit `MaxIterations` with no useful output. This fails silently — the sleep cycle "completes" but no skills or profile are updated.

### Impact

- Sleep mode appears to work but produces no learning.
- No error or warning is logged.
- User has no indication that self-improvement is broken.

### Files involved

- `internal/learning/sleep_manager.go` — `runLearningCycleForSessionsWithModel`
- `internal/capabilities/capabilities.go` — capability checking
- `internal/cache/cache.go` — `Checker` function
- `internal/probe/probe.go` — `Runner.Tools`

### Implementation plan

1. At the start of `runLearningCycleForSessionsWithModel`, after resolving `learningModel`, check if the model supports tools using `cache.Checker(SnapshotPath(""))` (same pattern used in `engine.go:152` for vision/audio capability checks).
2. If the snapshot cache is not available or the model is not in it, run a lightweight `probe.Runner.Tools(ctx, model)` call with a short timeout (30s). Cache the result.
3. If the model does not support tools, log a warning: `[sleep] Learning model %q does not support tools, skipping reflection cycle`. Skip the cycle entirely.
4. Optionally: surface this in the web UI settings page as a warning badge next to `OLLAMA_MODEL_LEARNING`.
5. Add a test mock verifying that the sleep cycle is skipped when the model lacks tool support.

---

## 4. User Feedback Loop for Reflector

**Priority: Low | Effort: M**

### Problem

The sleep mode reflector (`@/internal/learning/sleep_manager.go:488`) learns by observing conversation patterns. There is no mechanism for the user to explicitly tell the agent "you did this wrong" or "remember this preference". Learning is passive — it depends entirely on the reflector's interpretation of conversation history.

### Impact

- The agent may miss corrections that the user expresses informally.
- Users cannot directly shape the agent's skill set or profile.
- Learning cycles may focus on the wrong patterns.

### Files involved

- `internal/learning/sleep_manager.go` — `runLearningCycleForSessionsWithModel`, system prompt
- `internal/web/server.go` — API routes
- `internal/telegram/bot.go` — command handling
- `internal/sessions/` — feedback storage

### Implementation plan

1. Create a `FeedbackEntry` struct in `internal/learning/feedback.go`:
   ```go
   type FeedbackEntry struct {
       Text      string    `json:"text"`
       Category  string    `json:"category"` // "correction", "preference", "praise"
       CreatedAt time.Time `json:"created_at"`
   }
   ```
2. Add `SaveFeedback` and `LoadFeedback` functions that read/write `sessions/feedback.json`.
3. In `runLearningCycleForSessionsWithModel`, load feedback entries and append them to the `analysisPrompt` under a `## User Feedback` section. Clear the feedback file after processing.
4. Add `POST /api/feedback` endpoint in `internal/web/server.go` accepting `{ "text": "...", "category": "..." }`.
5. Add `/feedback <text>` command in `internal/telegram/bot.go` that saves feedback via the same path.
6. Add a simple feedback input in the web UI (e.g., a button in the settings or sidebar that opens a text input).
7. Update the reflector system prompt (`:488-511`) to instruct it to prioritize user feedback over observational patterns.

---

## 5. AutonomousManager Not Started in Telegram-Only Mode

**Priority: High | Effort: S**

### Problem

In `cmd/ollamabot/main.go:153`, `autoMgr` is created but `Start()` is never called directly. It is only started when the web server calls `srv.SetAutonomousManager(autoMgr)` → `server.Start()` → `s.autoMgr.Start()` (`@/internal/web/server.go:259`). The Telegram bot creates its own `autoMgr` in `bot.go:272` but never calls `Start()` on it. When Telegram is the only enabled channel (`SERVER_ENABLED=false`), autonomous projects never tick.

### Impact

- Users running Telegram-only mode cannot use autonomous projects.
- Projects are created but never execute — they stay in "pending" forever.
- This is a functional bug, not a design issue.

### Files involved

- `cmd/ollamabot/main.go` — `run` function (`:73`)
- `internal/telegram/bot.go` — `NewBotWithEnv` (`:265`)
- `internal/agent/autonomous.go` — `Start` method

### Implementation plan

1. In `cmd/ollamabot/main.go`, after creating `autoMgr` at line 153, add `autoMgr.Start(ctx)` and `defer autoMgr.Stop()` immediately after.
2. Remove the `autoMgr.Start()` call from `internal/web/server.go:259` to avoid double-starting. `AutonomousManager.Start` already guards against double-start (`@/internal/agent/autonomous.go:88-99` checks `cancelFunc != nil`), but removing the redundant call keeps the lifecycle clear.
3. In `internal/telegram/bot.go`, remove the `autoMgr` field from the `Bot` struct and `NewBotWithEnv` constructor. Instead, accept `*agent.AutonomousManager` via a `SetAutonomousManager` setter, matching the pattern used by the web server.
4. In `main.go`, when starting Telegram (both the background and foreground paths), call `bot.SetAutonomousManager(autoMgr)` before `bot.Start()`.
5. In `runServe` (`:344`), apply the same change: call `autoMgr.Start(ctx)` before starting the server, and remove the start from `server.Start()`.
6. Verify: the `AutonomousManager` is started once in `main.go`/`runServe`, shared across channels, and stopped on process exit.

---

## 6. Goal Persistence Across Restarts

**Priority: Medium | Effort: S**

### Problem

`GoalManager.activeLoops` (`@/internal/agent/goal.go:28`) is an in-memory map of cancel functions. `ResumeActiveGoals` (`:150`) iterates sessions with `GoalStatus == "active"` and restarts their loops. However, if the process dies mid-cycle, the session's `GoalStatus` remains `"active"` but the in-flight agent turn was interrupted. On restart, `runGoalLoop` resumes from the last saved session state, which may be inconsistent (partial tool output, missing assistant response).

### Impact

- After a crash, goals resume from a potentially inconsistent state.
- The agent may re-execute already-completed steps.
- In rare cases, the goal loop may get stuck in a retry loop.

### Files involved

- `internal/agent/goal.go` — `ResumeActiveGoals`, `runGoalLoop`
- `internal/sessions/` — session state, `GoalStatus` field

### Implementation plan

1. In `ResumeActiveGoals` (`@/internal/agent/goal.go:150`), before restarting a goal loop, check if the session is marked as `IsProcessing(sessionID)`. If so, it means the process died mid-turn.
2. For sessions in this state, append a system message to the session history: `"Previous cycle was interrupted due to a process restart. Review the conversation state and continue from where you left off."` This gives the agent context about the interruption.
3. Call `sessions.MarkIdle(sessionID)` to clear the stale processing flag before starting the new loop.
4. In `runGoalLoop` (`:266`), at the start of each cycle, save the session after reloading it (`:283`). This ensures that if the process crashes again, the last known state is persisted.
5. Add a `GoalRestartCount` field to the session metadata. In `ResumeActiveGoals`, if `GoalRestartCount > 3`, log a warning and set `GoalStatus` to `"paused"` instead of resuming. This prevents infinite restart loops for goals that consistently crash.
6. Add a test in `internal/agent/goal_test.go` verifying that `ResumeActiveGoals` clears stale processing flags and appends the restart notification message.
