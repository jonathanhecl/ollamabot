package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

type discardHandler struct{}

func (d *discardHandler) OnThinking(string)                 {}
func (d *discardHandler) OnContent(string)                  {}
func (d *discardHandler) OnToolCall(ollama.ToolCall)        {}
func (d *discardHandler) OnToolStart(string, any)             {}
func (d *discardHandler) OnToolResult(string, string)       {}
func (d *discardHandler) OnMediaPreProcessing(string)       {}
func (d *discardHandler) OnDone(ollama.ChatResponse)        {}

func TestRun_textOnlyTelegramPath(t *testing.T) {
	client := ollama.NewClient("http://127.0.0.1:11434")
	ctx := context.Background()
	if _, err := client.Version(ctx); err != nil {
		t.Skip("Ollama not available:", err)
	}

	model := "granite4.1:8b"
	cfg := config.Config{Workspace: t.TempDir()}
	registry := tools.NewRegistry(false, cfg.Workspace, nil, client, "", tools.SearchConfig{})
	a := agent.NewAgent(cfg, client, registry)

	msgs := []ollama.Message{{Role: "user", Content: "Reply with exactly: pong"}}
	final, err := a.Run(ctx, model, msgs, false, &discardHandler{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var answer string
	for i := len(final) - 1; i >= 0; i-- {
		if final[i].Role == "assistant" && strings.TrimSpace(final[i].Content) != "" {
			answer = final[i].Content
			break
		}
	}
	if answer == "" {
		t.Fatalf("empty assistant answer, history len=%d", len(final))
	}
	t.Logf("answer: %s", answer)
}
