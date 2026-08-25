package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestCollectorRecordTurn(t *testing.T) {
	c := NewCollector(5)

	m1 := sessions.Metrics{
		LoadDuration:       50 * 1e6,  // 50ms
		PromptEvalCount:    100,
		PromptEvalDuration: 150 * 1e6, // 150ms
		EvalCount:          50,
		EvalDuration:       1 * 1e9,   // 1s -> 50 TPS
	}
	c.RecordTurn("qwen2.5-coder:7b", "web", m1, 1200)

	snap := c.Snapshot(context.Background(), nil)
	if snap.TotalTurns != 1 {
		t.Fatalf("expected 1 total turn, got %d", snap.TotalTurns)
	}
	if snap.TotalPromptTokens != 100 {
		t.Fatalf("expected 100 prompt tokens, got %d", snap.TotalPromptTokens)
	}
	if snap.TotalEvalTokens != 50 {
		t.Fatalf("expected 50 eval tokens, got %d", snap.TotalEvalTokens)
	}
	if snap.OverallAvgTPS < 49.0 || snap.OverallAvgTPS > 51.0 {
		t.Fatalf("expected TPS around 50, got %f", snap.OverallAvgTPS)
	}

	if len(snap.Models) != 1 {
		t.Fatalf("expected 1 model stat, got %d", len(snap.Models))
	}
	if snap.Models[0].Model != "qwen2.5-coder:7b" {
		t.Fatalf("expected model name 'qwen2.5-coder:7b', got %s", snap.Models[0].Model)
	}
	if snap.Models[0].AvgTTFTMs != 200.0 {
		t.Fatalf("expected TTFT 200ms, got %f", snap.Models[0].AvgTTFTMs)
	}
}

func TestCollectorRecordTool(t *testing.T) {
	c := NewCollector(10)

	c.RecordTool("read_file", 45, false)
	c.RecordTool("read_file", 55, false)
	c.RecordTool("read_file", 20, true)

	snap := c.Snapshot(context.Background(), nil)
	if len(snap.Tools) != 1 {
		t.Fatalf("expected 1 tool stat, got %d", len(snap.Tools))
	}
	ts := snap.Tools[0]
	if ts.ToolName != "read_file" {
		t.Fatalf("expected tool name 'read_file', got %s", ts.ToolName)
	}
	if ts.ExecutionCount != 3 {
		t.Fatalf("expected 3 executions, got %d", ts.ExecutionCount)
	}
	if ts.ErrorCount != 1 {
		t.Fatalf("expected 1 error, got %d", ts.ErrorCount)
	}
	if ts.TotalDurationMs != 120 {
		t.Fatalf("expected 120 total ms, got %d", ts.TotalDurationMs)
	}
	if ts.AvgDurationMs != 40.0 {
		t.Fatalf("expected 40 avg ms, got %f", ts.AvgDurationMs)
	}
}

func TestCollectorOptimizationAndReset(t *testing.T) {
	c := NewCollector(5)
	c.RecordOptimization(1500)
	c.RecordOptimization(500)

	snap := c.Snapshot(context.Background(), nil)
	if snap.ContextOptimizations.Count != 2 {
		t.Fatalf("expected 2 optimizations, got %d", snap.ContextOptimizations.Count)
	}
	if snap.ContextOptimizations.TotalTokensSaved != 2000 {
		t.Fatalf("expected 2000 tokens saved, got %d", snap.ContextOptimizations.TotalTokensSaved)
	}

	c.Reset()
	snapAfter := c.Snapshot(context.Background(), nil)
	if snapAfter.ContextOptimizations.Count != 0 || snapAfter.TotalTurns != 0 {
		t.Fatalf("expected 0 stats after reset, got %+v", snapAfter)
	}
}

func TestCollectorVRAMSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ps" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollama.PsResponse{
				Models: []ollama.RunningModel{
					{
						Name:      "qwen2.5-coder:7b",
						SizeVRAM:  4500 * 1024 * 1024,
						ExpiresAt: time.Now().Add(5 * time.Minute).Format(time.RFC3339),
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := ollama.NewClient(server.URL)
	c := NewCollector(5)

	snap := c.Snapshot(context.Background(), client)
	if len(snap.ActiveVRAMModels) != 1 {
		t.Fatalf("expected 1 active VRAM model, got %d", len(snap.ActiveVRAMModels))
	}
	if snap.ActiveVRAMModels[0].Name != "qwen2.5-coder:7b" {
		t.Fatalf("expected 'qwen2.5-coder:7b', got %s", snap.ActiveVRAMModels[0].Name)
	}
	if snap.TotalVRAMUsedMB != 4500 {
		t.Fatalf("expected 4500 MB VRAM, got %d", snap.TotalVRAMUsedMB)
	}
}
