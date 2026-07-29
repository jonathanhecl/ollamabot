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
	// Type selects the transport. Valid values: "" / "stdio" (default),
	// "http" (Streamable HTTP), "sse" (legacy HTTP+SSE). When empty, stdio
	// is assumed and Command is required.
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// InsecureTLS skips TLS certificate verification for remote transports.
	// Useful for local servers with self-signed certs (e.g. Obsidian on 127.0.0.1).
	InsecureTLS bool     `json:"insecure_tls,omitempty"`
	Safe        bool     `json:"safe,omitempty"`
	SafeTools   []string `json:"safeTools,omitempty"`
}

type ServerSafety struct {
	Safe      bool
	SafeTools map[string]bool
}

type Manager struct {
	configPath   string
	clients      map[string]*Client
	tools        map[string]MCPTool      // toolName -> tool definition
	toolServer   map[string]string       // toolName -> serverName
	safety       map[string]ServerSafety // serverName -> safety settings
	statusErrors map[string]string       // serverName -> last start error
	mu           sync.RWMutex
}

func NewManager(configPath string) *Manager {
	return &Manager{
		configPath:   configPath,
		clients:      make(map[string]*Client),
		tools:        make(map[string]MCPTool),
		toolServer:   make(map[string]string),
		safety:       make(map[string]ServerSafety),
		statusErrors: make(map[string]string),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startNoLock(ctx)
}

func (m *Manager) startNoLock(ctx context.Context) error {
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
		m.startServerNoLock(ctx, name, srvCfg)
	}

	return nil
}

// startServerNoLock starts a single server and registers its tools. Failures
// are recorded in statusErrors without affecting other servers.
func (m *Manager) startServerNoLock(ctx context.Context, name string, srvCfg ServerConfig) {
	log.Printf("[MCP] Initializing server %q (transport=%s)", name, transportLabel(srvCfg.Type))
	client, err := NewClient(name, srvCfg)
	if err != nil {
		m.statusErrors[name] = err.Error()
		log.Printf("[MCP] Error building client for server %q: %v. Continuing with other servers.", name, err)
		return
	}
	if err := client.Start(ctx); err != nil {
		m.statusErrors[name] = err.Error()
		log.Printf("[MCP] Error starting server %q: %v. Continuing with other servers.", name, err)
		return
	}

	// Query tools before registering, to avoid a half-working server.
	var listResult ListToolsResult
	if err := client.Call(ctx, "tools/list", nil, &listResult); err != nil {
		_ = client.Close()
		m.statusErrors[name] = err.Error()
		log.Printf("[MCP] Error listing tools for server %q: %v", name, err)
		return
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

	// Clear any prior error once the server is healthy.
	delete(m.statusErrors, name)

	for _, tool := range listResult.Tools {
		m.tools[tool.Name] = tool
		m.toolServer[tool.Name] = name
		log.Printf("[MCP] Registered tool %q from server %q (safe: %v)", tool.Name, name, m.isSafeNoLock(name, tool.Name))
	}
}

// stopServerNoLock stops a single server and unregisters its tools, leaving
// all other servers untouched.
func (m *Manager) stopServerNoLock(name string) {
	if client, ok := m.clients[name]; ok {
		log.Printf("[MCP] Stopping server %q...", name)
		_ = client.Close()
		delete(m.clients, name)
	}
	for toolName, srv := range m.toolServer {
		if srv == name {
			delete(m.toolServer, toolName)
			delete(m.tools, toolName)
		}
	}
	delete(m.safety, name)
	delete(m.statusErrors, name)
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopNoLock()
}

func (m *Manager) stopNoLock() {
	for name, client := range m.clients {
		log.Printf("[MCP] Stopping server %q...", name)
		_ = client.Close()
	}
	m.clients = make(map[string]*Client)
	m.tools = make(map[string]MCPTool)
	m.toolServer = make(map[string]string)
	m.safety = make(map[string]ServerSafety)
	m.statusErrors = make(map[string]string)
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

// GetToolServer returns the MCP server name that provides the given tool, or
// an empty string if the tool is not registered from an MCP server.
func (m *Manager) GetToolServer(toolName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.toolServer[toolName]
}

type MCPServerStatus struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	InsecureTLS bool              `json:"insecure_tls,omitempty"`
	Error       string            `json:"error,omitempty"`
	Safe        bool              `json:"safe"`
	SafeTools   []string          `json:"safeTools,omitempty"`
	Status      string            `json:"status"` // "running", "stopped"
	Tools       []MCPTool         `json:"tools,omitempty"`
}

func (m *Manager) GetServersStatus() (map[string]MCPServerStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path := m.configPath
	if !filepath.IsAbs(path) {
		if exe, err := os.Executable(); err == nil {
			path = filepath.Join(filepath.Dir(exe), m.configPath)
		}
	}

	var cfg ConfigFile
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]MCPServerStatus), nil
		}
		return nil, fmt.Errorf("failed to open config: %w", err)
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	result := make(map[string]MCPServerStatus)
	for name, srvCfg := range cfg.McpServers {
		status := "stopped"
		if _, active := m.clients[name]; active {
			status = "running"
		}

		var tools []MCPTool
		for toolName, t := range m.tools {
			if m.toolServer[toolName] == name {
				tools = append(tools, t)
			}
		}

		errMsg := m.statusErrors[name]
		result[name] = MCPServerStatus{
			Name:        name,
			Type:        srvCfg.Type,
			Command:     srvCfg.Command,
			Args:        srvCfg.Args,
			Env:         srvCfg.Env,
			URL:         srvCfg.URL,
			Headers:     srvCfg.Headers,
			InsecureTLS: srvCfg.InsecureTLS,
			Error:       errMsg,
			Safe:        srvCfg.Safe,
			SafeTools:   srvCfg.SafeTools,
			Status:      status,
			Tools:       tools,
		}
	}

	return result, nil
}

// ValidationError indicates the server config is reachable but the MCP
// handshake or tools/list call failed. It is surfaced as a 4xx to the user
// rather than an internal server error.
type ValidationError struct {
	Server string
	Err    error
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("MCP server %q validation failed: %v", v.Server, v.Err)
}

func (v *ValidationError) Unwrap() error {
	return v.Err
}

func (m *Manager) AddOrUpdateServer(ctx context.Context, name string, srvCfg ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// First validate the server: can we start it and list tools? This avoids
	// persisting a broken configuration that then appears as "stopped".
	if err := m.validateServerNoLock(ctx, name, srvCfg); err != nil {
		log.Printf("[MCP] Validation failed for server %q: %v", name, err)
		return &ValidationError{Server: name, Err: err}
	}

	path := m.configPath
	if !filepath.IsAbs(path) {
		if exe, err := os.Executable(); err == nil {
			path = filepath.Join(filepath.Dir(exe), m.configPath)
		}
	}

	var cfg ConfigFile
	file, err := os.Open(path)
	if err == nil {
		_ = json.NewDecoder(file).Decode(&cfg)
		file.Close()
	}

	if cfg.McpServers == nil {
		cfg.McpServers = make(map[string]ServerConfig)
	}

	cfg.McpServers[name] = srvCfg

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write mcp config: %w", err)
	}

	// Restart only the affected server; other servers keep their connections.
	m.stopServerNoLock(name)
	m.startServerNoLock(ctx, name, srvCfg)
	return nil
}

// validateServerNoLock attempts to start the given configuration and read its
// tool list without mutating the manager's active state. It returns an error if
// the server cannot be reached or does not respond correctly.
func (m *Manager) validateServerNoLock(ctx context.Context, name string, srvCfg ServerConfig) error {
	client, err := NewClient(name, srvCfg)
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	defer client.Close()

	var listResult ListToolsResult
	if err := client.Call(ctx, "tools/list", nil, &listResult); err != nil {
		return err
	}
	return nil
}

// transportLabel returns a human-readable transport name for logs.
func transportLabel(t string) string {
	if t == "" {
		return "stdio"
	}
	return t
}

func (m *Manager) DeleteServer(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := m.configPath
	if !filepath.IsAbs(path) {
		if exe, err := os.Executable(); err == nil {
			path = filepath.Join(filepath.Dir(exe), m.configPath)
		}
	}

	var cfg ConfigFile
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open config: %w", err)
	}
	err = json.NewDecoder(file).Decode(&cfg)
	file.Close()
	if err != nil {
		return fmt.Errorf("failed to decode config: %w", err)
	}

	if cfg.McpServers == nil {
		return fmt.Errorf("server %q not found", name)
	}

	if _, exists := cfg.McpServers[name]; !exists {
		return fmt.Errorf("server %q not found", name)
	}

	delete(cfg.McpServers, name)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write mcp config: %w", err)
	}

	m.stopServerNoLock(name)
	return nil
}
