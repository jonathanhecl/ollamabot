package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func deviceToolCall(name string) ollama.ToolCall {
	args, _ := json.Marshal(map[string]any{"paths": []string{"report.md"}})
	return ollama.ToolCall{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:      name,
			Arguments: args,
		},
	}
}

// The device channel is audio-only: tools that need a UI or deliver artifacts
// must not reach the model, or it loops on calls it can never complete.
func TestDeviceChannelHidesInteractiveTools(t *testing.T) {
	registry := NewRegistry(false, ".", nil, nil, "", SearchConfig{})
	registry.SetChannel("device")

	for _, name := range []string{"ask_clarification", "present_plan", "send_voice", "send_files"} {
		if hasTool(registry, name) {
			t.Fatalf("%s should not be offered on the device channel", name)
		}
		if _, err := registry.Execute(context.Background(), deviceToolCall(name)); err == nil {
			t.Fatalf("%s should be rejected on the device channel", name)
		}
	}

	// A normal channel keeps them.
	registry.SetChannel("web")
	if !hasTool(registry, "send_files") {
		t.Fatal("send_files should be offered outside the device channel")
	}
}
