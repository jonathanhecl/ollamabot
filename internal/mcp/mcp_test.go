package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}

		var resp JSONRPCResponse
		resp.Jsonrpc = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "initialize":
			res := InitializeResult{
				ProtocolVersion: "2024-11-05",
				ServerInfo: ServerInfo{
					Name:    "mock-server",
					Version: "1.0.0",
				},
			}
			resBytes, _ := json.Marshal(res)
			resp.Result = resBytes
		case "notifications/initialized":
			continue
		case "tools/list":
			res := ListToolsResult{
				Tools: []MCPTool{
					{
						Name:        "echo",
						Description: "Echo input",
						InputSchema: MCPInputSchema{
							Type: "object",
							Properties: map[string]any{
								"msg": map[string]any{"type": "string"},
							},
						},
					},
					{
						Name:        "risky_cmd",
						Description: "A risky tool",
						InputSchema: MCPInputSchema{
							Type: "object",
						},
					},
				},
			}
			resBytes, _ := json.Marshal(res)
			resp.Result = resBytes
		case "tools/call":
			var params CallToolParams
			_ = json.Unmarshal(req.Params, &params)
			if params.Name == "echo" {
				res := CallToolResult{
					Content: []MCPContent{
						{
							Type: "text",
							Text: fmt.Sprintf("echo: %v", params.Arguments["msg"]),
						},
					},
				}
				resBytes, _ := json.Marshal(res)
				resp.Result = resBytes
			} else {
				res := CallToolResult{
					Content: []MCPContent{
						{
							Type: "text",
							Text: "risky done",
						},
					},
				}
				resBytes, _ := json.Marshal(res)
				resp.Result = resBytes
			}
		default:
			resp.Error = &JSONRPCError{
				Code:    -32601,
				Message: "Method not found",
			}
		}

		respBytes, _ := json.Marshal(resp)
		os.Stdout.Write(append(respBytes, '\n'))
	}
}

func TestClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient("test", os.Args[0], []string{"-test.run=TestHelperProcess"}, map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
	})

	if err := client.Start(ctx); err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	var listResult ListToolsResult
	if err := client.Call(ctx, "tools/list", nil, &listResult); err != nil {
		t.Fatalf("tools/list call failed: %v", err)
	}

	if len(listResult.Tools) != 2 || listResult.Tools[0].Name != "echo" {
		t.Errorf("unexpected tools: %+v", listResult.Tools)
	}

	var callResult CallToolResult
	callParams := CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"msg": "hello"},
	}
	if err := client.Call(ctx, "tools/call", callParams, &callResult); err != nil {
		t.Fatalf("tools/call call failed: %v", err)
	}

	if len(callResult.Content) != 1 || callResult.Content[0].Text != "echo: hello" {
		t.Errorf("unexpected call result: %+v", callResult)
	}
}

func TestManager(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcp-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := fmt.Sprintf(`{
		"mcpServers": {
			"mock": {
				"command": %q,
				"args": ["-test.run=TestHelperProcess"],
				"env": {
					"GO_WANT_HELPER_PROCESS": "1"
				},
				"safe": false,
				"safeTools": ["echo"]
			}
		}
	}`, os.Args[0])

	configPath := filepath.Join(tmpDir, "mcp_config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	manager := NewManager(configPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer manager.Stop()

	tools := manager.GetTools()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	if !manager.HasTool("echo") || !manager.HasTool("risky_cmd") {
		t.Error("expected tools 'echo' and 'risky_cmd' to exist")
	}

	if !manager.IsSafe("echo") {
		t.Error("expected 'echo' to be safe")
	}

	if manager.IsSafe("risky_cmd") {
		t.Error("expected 'risky_cmd' not to be safe")
	}

	res, err := manager.Execute(ctx, "echo", map[string]any{"msg": "world"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res != "echo: world" {
		t.Errorf("expected 'echo: world', got %q", res)
	}

	// Test GetServersStatus
	status, err := manager.GetServersStatus()
	if err != nil {
		t.Fatalf("GetServersStatus failed: %v", err)
	}
	if len(status) != 1 {
		t.Errorf("expected status for 1 server, got %d", len(status))
	}
	mockStatus, ok := status["mock"]
	if !ok {
		t.Error("expected 'mock' server status to exist")
	} else {
		if mockStatus.Status != "running" {
			t.Errorf("expected 'mock' status running, got %q", mockStatus.Status)
		}
		if len(mockStatus.Tools) != 2 {
			t.Errorf("expected 2 tools on server status, got %d", len(mockStatus.Tools))
		}
	}

	// Test AddOrUpdateServer (updating existing mock to be safe)
	newSrv := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Safe:    true,
	}
	if err := manager.AddOrUpdateServer(ctx, "mock", newSrv); err != nil {
		t.Fatalf("AddOrUpdateServer failed: %v", err)
	}

	status, err = manager.GetServersStatus()
	if err != nil {
		t.Fatalf("GetServersStatus failed: %v", err)
	}
	if len(status) != 1 {
		t.Errorf("expected status for 1 server, got %d", len(status))
	}
	if !manager.IsSafe("risky_cmd") {
		t.Error("expected 'risky_cmd' to be safe after updating mock to be safe")
	}

	// Test AddOrUpdateServer (adding a new server to delete)
	tempSrv := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Safe:    true,
	}
	if err := manager.AddOrUpdateServer(ctx, "temp_mock", tempSrv); err != nil {
		t.Fatalf("AddOrUpdateServer failed: %v", err)
	}

	status, err = manager.GetServersStatus()
	if err != nil {
		t.Fatalf("GetServersStatus failed: %v", err)
	}
	if len(status) != 2 {
		t.Errorf("expected status for 2 servers after adding, got %d", len(status))
	}

	// Test DeleteServer
	if err := manager.DeleteServer(ctx, "temp_mock"); err != nil {
		t.Fatalf("DeleteServer failed: %v", err)
	}

	status, err = manager.GetServersStatus()
	if err != nil {
		t.Fatalf("GetServersStatus failed: %v", err)
	}
	if len(status) != 1 {
		t.Errorf("expected status for 1 server after deletion, got %d", len(status))
	}
	if _, ok := status["temp_mock"]; ok {
		t.Error("expected 'temp_mock' to be deleted from status")
	}
}
