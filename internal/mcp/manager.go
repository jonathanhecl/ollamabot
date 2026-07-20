package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type ConfigFile struct {
	McpServers map[string]ServerConfig `json:"mcpServers"`
}

type ServerConfig struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env,omitempty"`
	Safe      bool              `json:"safe,omitempty"`
	SafeTools []string          `json:"safeTools,omitempty"`
}

type ServerSafety struct {
	Safe      bool
	SafeTools map[string]bool
}

type Manager struct {
	configPath string
	clients    map[string]*Client
	tools      map[string]MCPTool      // toolName -> tool definition
	toolServer map[string]string       // toolName -> serverName
	safety     map[string]ServerSafety // serverName -> safety settings
	mu         sync.RWMutex
}

func NewManager(configPath string) *Manager {
	return &Manager{
		configPath: configPath,
		clients:    make(map[string]*Client),
		tools:      make(map[string]MCPTool),
		toolServer: make(map[string]string),
		safety:     make(map[string]ServerSafety),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := m.configPath
	if !filepath.IsAbs(path) {
		if exe, err := os.Executable(); err == nil {
			path = filepath.Join(filepath.Dir(exe), m.configPath)
		}
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("[MCP] Config file %s not found. Skipping MCP setup.", m.configPath)
			return nil
		}
		return fmt.Errorf("failed to open mcp config: %w", err)
	}
	defer file.Close()

	var cfg ConfigFile
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return fmt.Errorf("failed to parse mcp config: %w", err)
	}

	log.Printf("[MCP] Loaded %d server(s) from config", len(cfg.McpServers))

	for name, srvCfg := range cfg.McpServers {
		log.Printf("[MCP] Initializing server %q: %s %v", name, srvCfg.Command, srvCfg.Args)
		client := NewClient(name, srvCfg.Command, srvCfg.Args, srvCfg.Env)
		if err := client.Start(ctx); err != nil {
			log.Printf("[MCP] Error starting server %q: %v. Continuing with other servers.", name, err)
			continue
		}

		m.clients[name] = client

		// Setup safety configuration
		safety := ServerSafety{
			Safe:      srvCfg.Safe,
			SafeTools: make(map[string]bool),
		}
		for _, t := range srvCfg.SafeTools {
			safety.SafeTools[t] = true
		}
		m.safety[name] = safety

		// Query tools
		var listResult ListToolsResult
		if err := client.Call(ctx, "tools/list", nil, &listResult); err != nil {
			log.Printf("[MCP] Error listing tools for server %q: %v", name, err)
			continue
		}

		for _, tool := range listResult.Tools {
			m.tools[tool.Name] = tool
			m.toolServer[tool.Name] = name
			log.Printf("[MCP] Registered tool %q from server %q (safe: %v)", tool.Name, name, m.isSafeNoLock(name, tool.Name))
		}
	}

	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, client := range m.clients {
		log.Printf("[MCP] Stopping server %q...", name)
		_ = client.Close()
	}
	m.clients = make(map[string]*Client)
	m.tools = make(map[string]MCPTool)
	m.toolServer = make(map[string]string)
	m.safety = make(map[string]ServerSafety)
}

func (m *Manager) GetTools() []MCPTool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []MCPTool
	for _, t := range m.tools {
		list = append(list, t)
	}
	return list
}

func (m *Manager) IsSafe(toolName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	serverName, ok := m.toolServer[toolName]
	if !ok {
		return false
	}
	return m.isSafeNoLock(serverName, toolName)
}

func (m *Manager) isSafeNoLock(serverName, toolName string) bool {
	safety, ok := m.safety[serverName]
	if !ok {
		return false
	}
	if safety.Safe {
		return true
	}
	return safety.SafeTools[toolName]
}

func (m *Manager) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	serverName, ok := m.toolServer[toolName]
	client, okClient := m.clients[serverName]
	m.mu.RUnlock()

	if !ok || !okClient {
		return "", fmt.Errorf("mcp server or tool %q not found", toolName)
	}

	var callResult CallToolResult
	params := CallToolParams{
		Name:      toolName,
		Arguments: args,
	}

	if err := client.Call(ctx, "tools/call", params, &callResult); err != nil {
		return "", fmt.Errorf("mcp execution failed: %w", err)
	}

	var sb strings.Builder
	for _, content := range callResult.Content {
		if content.Type == "text" {
			sb.WriteString(content.Text)
		}
	}

	output := sb.String()
	if callResult.IsError {
		return output, fmt.Errorf("tool returned error: %s", output)
	}

	return output, nil
}

func (m *Manager) HasTool(toolName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.tools[toolName]
	return ok
}
