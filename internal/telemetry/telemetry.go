package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

// TurnRecord holds summary performance info for a single completed turn.
type TurnRecord struct {
	Timestamp       time.Time `json:"timestamp"`
	Model           string    `json:"model"`
	Channel         string    `json:"channel"`
	DurationMs      int64     `json:"duration_ms"`
	PromptTokens    int       `json:"prompt_tokens"`
	EvalTokens      int       `json:"eval_tokens"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	TTFTMs          int64     `json:"ttft_ms"`
}

// ModelStats aggregates metrics for a specific model name.
type ModelStats struct {
	Model               string  `json:"model"`
	TotalTurns          int64   `json:"total_turns"`
	TotalPromptTokens   int64   `json:"total_prompt_tokens"`
	TotalEvalTokens     int64   `json:"total_eval_tokens"`
	TotalEvalDurationNs int64   `json:"total_eval_duration_ns"`
	AvgTokensPerSecond  float64 `json:"avg_tokens_per_second"`
	AvgTTFTMs           float64 `json:"avg_ttft_ms"`
	totalTTFTMs         int64
}

// ToolStats aggregates metrics for an individual tool.
type ToolStats struct {
	ToolName        string  `json:"tool_name"`
	ExecutionCount  int64   `json:"execution_count"`
	ErrorCount      int64   `json:"error_count"`
	TotalDurationMs int64   `json:"total_duration_ms"`
	AvgDurationMs   float64 `json:"avg_duration_ms"`
}

// OptimizationStats tracks context compaction activity.
type OptimizationStats struct {
	Count               int64 `json:"count"`
	TotalTokensSaved    int64 `json:"total_tokens_saved"`
	LastOptimizationSec int64 `json:"last_optimization_unix,omitempty"`
}

// VRAMModelInfo represents model memory usage from Ollama.
type VRAMModelInfo struct {
	Name          string `json:"name"`
	SizeVRAMBytes int64  `json:"size_vram_bytes"`
	SizeVRAMMB    int64  `json:"size_vram_mb"`
	ExpiresAt     string `json:"expires_at"`
}

// Snapshot represents a point-in-time telemetry snapshot of the agent process.
type Snapshot struct {
	UptimeSeconds        int64              `json:"uptime_seconds"`
	TotalTurns           int64              `json:"total_turns"`
	TotalPromptTokens    int64              `json:"total_prompt_tokens"`
	TotalEvalTokens      int64              `json:"total_eval_tokens"`
	OverallAvgTPS        float64            `json:"overall_avg_tps"`
	Models               []ModelStats       `json:"models"`
	Tools                []ToolStats        `json:"tools"`
	ContextOptimizations OptimizationStats  `json:"context_optimizations"`
	ActiveVRAMModels     []VRAMModelInfo    `json:"active_vram_models"`
	TotalVRAMUsedMB      int64              `json:"total_vram_used_mb"`
	RecentTurns          []TurnRecord       `json:"recent_turns"`
}

// Collector manages in-memory performance telemetry for the agent system.
type Collector struct {
	mu           sync.RWMutex
	startTime    time.Time
	totalTurns   int64
	promptTokens int64
	evalTokens   int64
	evalDuration int64 // nanoseconds

	models      map[string]*ModelStats
	tools       map[string]*ToolStats
	optimizations OptimizationStats
	recentTurns []TurnRecord
	maxRecent   int
}

// Global is the default global telemetry collector instance.
var Global = NewCollector(50)

// NewCollector creates a new Telemetry Collector with a maximum recent turn history.
func NewCollector(maxRecent int) *Collector {
	if maxRecent <= 0 {
		maxRecent = 50
	}
	return &Collector{
		startTime:   time.Now(),
		models:      make(map[string]*ModelStats),
		tools:       make(map[string]*ToolStats),
		recentTurns: make([]TurnRecord, 0, maxRecent),
		maxRecent:   maxRecent,
	}
}

// RecordTurn records metrics from a completed assistant turn.
func (c *Collector) RecordTurn(model, channel string, m sessions.Metrics, durationMs int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalTurns++
	c.promptTokens += int64(m.PromptEvalCount)
	c.evalTokens += int64(m.EvalCount)
	c.evalDuration += m.EvalDuration

	// Calculate TPS for this turn
	var tps float64
	if m.EvalDuration > 0 && m.EvalCount > 0 {
		tps = float64(m.EvalCount) / (float64(m.EvalDuration) / 1e9)
	}

	// Calculate TTFT in ms (LoadDuration + PromptEvalDuration)
	ttftMs := (m.LoadDuration + m.PromptEvalDuration) / 1e6

	// Update per-model stats
	ms, ok := c.models[model]
	if !ok {
		ms = &ModelStats{Model: model}
		c.models[model] = ms
	}
	ms.TotalTurns++
	ms.TotalPromptTokens += int64(m.PromptEvalCount)
	ms.TotalEvalTokens += int64(m.EvalCount)
	ms.TotalEvalDurationNs += m.EvalDuration
	ms.totalTTFTMs += ttftMs

	if ms.TotalEvalDurationNs > 0 && ms.TotalEvalTokens > 0 {
		ms.AvgTokensPerSecond = float64(ms.TotalEvalTokens) / (float64(ms.TotalEvalDurationNs) / 1e9)
	}
	if ms.TotalTurns > 0 {
		ms.AvgTTFTMs = float64(ms.totalTTFTMs) / float64(ms.TotalTurns)
	}

	// Update recent turns ring buffer
	rec := TurnRecord{
		Timestamp:       time.Now(),
		Model:           model,
		Channel:         channel,
		DurationMs:      durationMs,
		PromptTokens:    m.PromptEvalCount,
		EvalTokens:      m.EvalCount,
		TokensPerSecond: tps,
		TTFTMs:          ttftMs,
	}

	if len(c.recentTurns) >= c.maxRecent {
		c.recentTurns = c.recentTurns[1:]
	}
	c.recentTurns = append(c.recentTurns, rec)
}

// RecordTool records a tool execution with duration and error status.
func (c *Collector) RecordTool(toolName string, durationMs int64, hasError bool) {
	if c == nil || toolName == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	ts, ok := c.tools[toolName]
	if !ok {
		ts = &ToolStats{ToolName: toolName}
		c.tools[toolName] = ts
	}
	ts.ExecutionCount++
	if hasError {
		ts.ErrorCount++
	}
	ts.TotalDurationMs += durationMs
	if ts.ExecutionCount > 0 {
		ts.AvgDurationMs = float64(ts.TotalDurationMs) / float64(ts.ExecutionCount)
	}
}

// RecordOptimization records context compaction events.
func (c *Collector) RecordOptimization(tokensSaved int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.optimizations.Count++
	c.optimizations.TotalTokensSaved += int64(tokensSaved)
	c.optimizations.LastOptimizationSec = time.Now().Unix()
}

// Snapshot returns a comprehensive point-in-time metrics summary, including active VRAM from Ollama.
func (c *Collector) Snapshot(ctx context.Context, client *ollama.Client) Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.RLock()
	snap := Snapshot{
		UptimeSeconds:        int64(time.Since(c.startTime).Seconds()),
		TotalTurns:           c.totalTurns,
		TotalPromptTokens:    c.promptTokens,
		TotalEvalTokens:      c.evalTokens,
		ContextOptimizations: c.optimizations,
		Models:               make([]ModelStats, 0, len(c.models)),
		Tools:                make([]ToolStats, 0, len(c.tools)),
		RecentTurns:          make([]TurnRecord, len(c.recentTurns)),
	}

	if c.evalDuration > 0 && c.evalTokens > 0 {
		snap.OverallAvgTPS = float64(c.evalTokens) / (float64(c.evalDuration) / 1e9)
	}

	for _, m := range c.models {
		snap.Models = append(snap.Models, *m)
	}
	for _, t := range c.tools {
		snap.Tools = append(snap.Tools, *t)
	}
	copy(snap.RecentTurns, c.recentTurns)
	c.mu.RUnlock()

	// Query Ollama for loaded models and VRAM if client is available
	if client != nil {
		psResp, err := client.Ps(ctx)
		if err == nil && len(psResp.Models) > 0 {
			var totalVRAM int64
			for _, m := range psResp.Models {
				mb := m.SizeVRAM / (1024 * 1024)
				totalVRAM += mb
				snap.ActiveVRAMModels = append(snap.ActiveVRAMModels, VRAMModelInfo{
					Name:          m.Name,
					SizeVRAMBytes: m.SizeVRAM,
					SizeVRAMMB:    mb,
					ExpiresAt:     m.ExpiresAt,
				})
			}
			snap.TotalVRAMUsedMB = totalVRAM
		}
	}

	return snap
}

// Reset clears all in-memory accumulated metrics.
func (c *Collector) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.startTime = time.Now()
	c.totalTurns = 0
	c.promptTokens = 0
	c.evalTokens = 0
	c.evalDuration = 0
	c.models = make(map[string]*ModelStats)
	c.tools = make(map[string]*ToolStats)
	c.optimizations = OptimizationStats{}
	c.recentTurns = make([]TurnRecord, 0, c.maxRecent)
}
