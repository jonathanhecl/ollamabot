package agent

import (
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func TestIsNoOpToolCall(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		params   map[string]any
		expected bool
	}{
		{"list default path dot", "list_files", map[string]any{"path": "."}, true},
		{"list empty path", "list_files", map[string]any{"path": ""}, true},
		{"list recursive false", "list_files", map[string]any{"recursive": false}, true},
		{"list real path", "list_files", map[string]any{"path": "src"}, false},
		{"read file", "read_file", map[string]any{"path": "foo.txt"}, false},
		{"search query", "web_search", map[string]any{"query": "hello"}, false},
		{"empty params", "some_tool", map[string]any{}, true},
	}
	for _, tc := range cases {
		if got := isNoOpToolCall(tc.tool, tc.params); got != tc.expected {
			t.Errorf("%s: isNoOpToolCall(%q, %v) = %v, want %v", tc.name, tc.tool, tc.params, got, tc.expected)
		}
	}
}

func TestIsNetworkFetchTool(t *testing.T) {
	if !isNetworkFetchTool("web_search") || !isNetworkFetchTool("fetch_webpage") {
		t.Fatal("expected web_search and fetch_webpage to be network tools")
	}
	if isNetworkFetchTool("read_file") || isNetworkFetchTool("execute_command") {
		t.Fatal("expected read_file/execute_command to not be network tools")
	}
}

func TestIsWorkspaceFileTool(t *testing.T) {
	for _, name := range []string{"read_file", "list_files", "write_file", "edit_file", "apply_diff", "search_files"} {
		if !isWorkspaceFileTool(name) {
			t.Fatalf("expected %s to be a workspace file tool", name)
		}
	}
	if isWorkspaceFileTool("web_search") || isWorkspaceFileTool("memory_add") {
		t.Fatal("expected web_search/memory_add to not be workspace file tools")
	}
}

func TestIsParallelSafeTool(t *testing.T) {
	for _, name := range []string{"read_file", "list_files", "search_files", "fetch_webpage", "web_search", "memory_search", "memory_list"} {
		if !isParallelSafeTool(name, map[string]any{}, "internal") {
			t.Fatalf("expected internal %s to be parallel-safe", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "apply_diff", "execute_command", "present_plan", "memory_add", "memory_delete", "generate_image"} {
		if isParallelSafeTool(name, map[string]any{}, "internal") {
			t.Fatalf("expected internal %s to be sequential", name)
		}
	}
	// MCP read-like tools are safe, write-like tools are sequential.
	if !isParallelSafeTool("vault_list", map[string]any{}, "mcp:obsidian") {
		t.Fatal("expected MCP list tool to be parallel-safe")
	}
	if isParallelSafeTool("vault_write", map[string]any{}, "mcp:obsidian") {
		t.Fatal("expected MCP write tool to be sequential")
	}
}

func TestRepetitiveLoopHint(t *testing.T) {
	// Hints are tool-specific and non-empty.
	for _, name := range []string{"list_files", "read_file", "web_search", "execute_command", "some_unknown_tool"} {
		if got := repetitiveLoopHint(name, nil); got == "" {
			t.Fatalf("expected a hint for %s", name)
		}
	}
	// Different tools get different hints.
	if repetitiveLoopHint("list_files", nil) == repetitiveLoopHint("read_file", nil) {
		t.Fatal("expected different hints for list_files and read_file")
	}
	// nil registry must not panic.
	_ = repetitiveLoopHint("vault_list", nil)
}

// TestLoopCallKeyUsesArguments guards the repetition detector: the approval
// grant key collapses most tools to "tool:*", which made two different URLs or
// two different search queries count as the same call and aborted the turn.
func TestLoopCallKeyUsesArguments(t *testing.T) {
	first := loopCallKey("fetch_webpage", map[string]any{"url": "https://a.example"})
	second := loopCallKey("fetch_webpage", map[string]any{"url": "https://b.example"})
	if first == second {
		t.Fatal("different URLs share a loop key")
	}
	same := loopCallKey("fetch_webpage", map[string]any{"url": "https://a.example"})
	if first != same {
		t.Fatal("identical calls do not share a loop key")
	}
	query1 := loopCallKey("web_search", map[string]any{"query": "clima hoy"})
	query2 := loopCallKey("web_search", map[string]any{"query": "temperatura"})
	if query1 == query2 {
		t.Fatal("different queries share a loop key")
	}
}

func TestSanitizeRequestMessages(t *testing.T) {
	// Case 1: Assistant message with tool calls followed by user message -> tool calls stripped
	input := []ollama.Message{
		{Role: "user", Content: "Hello"},
		{
			Role:    "assistant",
			Content: "Let me check",
			ToolCalls: []ollama.ToolCall{
				{Function: ollama.ToolFunction{Name: "list_files"}},
			},
		},
		{Role: "user", Content: "New question"},
	}

	sanitized := sanitizeRequestMessages(input)
	if len(sanitized[1].ToolCalls) != 0 {
		t.Fatalf("expected tool calls to be stripped when followed by user message, got %d", len(sanitized[1].ToolCalls))
	}

	// Case 2: Assistant message with tool calls followed by tool message -> tool calls preserved
	inputValid := []ollama.Message{
		{Role: "user", Content: "Hello"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ollama.ToolCall{
				{Function: ollama.ToolFunction{Name: "list_files"}},
			},
		},
		{Role: "tool", Name: "list_files", Content: "file1.txt"},
	}

	sanitizedValid := sanitizeRequestMessages(inputValid)
	if len(sanitizedValid[1].ToolCalls) != 1 {
		t.Fatalf("expected tool calls to be preserved when followed by tool message, got %d", len(sanitizedValid[1].ToolCalls))
	}

	// Case 3: Trailing assistant message with tool calls -> tool calls stripped and placeholder added
	inputTrailing := []ollama.Message{
		{Role: "user", Content: "Hello"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ollama.ToolCall{
				{Function: ollama.ToolFunction{Name: "list_files"}},
			},
		},
	}
	sanitizedTrailing := sanitizeRequestMessages(inputTrailing)
	if len(sanitizedTrailing[1].ToolCalls) != 0 {
		t.Fatalf("expected trailing assistant tool calls to be stripped, got %d", len(sanitizedTrailing[1].ToolCalls))
	}
	if sanitizedTrailing[1].Content == "" {
		t.Fatal("expected non-empty content after stripping trailing tool calls")
	}
}
