package mcp

import (
	"context"
	"fmt"
	"strings"
)

// transport abstracts the underlying MCP transport (stdio, http, sse).
type transport interface {
	// start prepares the transport (e.g. spawns the subprocess or opens the
	// SSE stream). It is called before the initialize handshake.
	start(ctx context.Context) error
	call(ctx context.Context, method string, params any, result interface{}) error
	notify(ctx context.Context, method string, params any) error
	close() error
}

// Client is a transport-agnostic MCP client. It performs the JSON-RPC
// initialize handshake on top of whatever transport is configured.
type Client struct {
	name      string
	transport transport
}

// NewClient builds a client for the given server configuration, selecting the
// appropriate transport based on cfg.Type. The supported types are:
//   - "" / "stdio": launches a local subprocess and talks JSON-RPC over
//     stdin/stdout (the classic MCP transport).
//   - "http": Streamable HTTP transport (MCP 2025-03-26). POSTs JSON-RPC
//     requests to cfg.URL and reads either a single JSON response or an
//     SSE stream.
//   - "sse": Legacy HTTP+SSE transport (MCP 2024-11-05). Opens a long-lived
//     SSE connection to cfg.URL and POSTs requests to the endpoint advertised
//     by the server.
func NewClient(name string, cfg ServerConfig) (*Client, error) {
	t, err := newTransport(name, cfg)
	if err != nil {
		return nil, err
	}
	return &Client{name: name, transport: t}, nil
}

// Start performs the initialize handshake. The transport must have been
// prepared (via its start method) before calling.
func (c *Client) Start(ctx context.Context) error {
	if err := c.transport.start(ctx); err != nil {
		return err
	}

	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "ollamabot",
			Version: "1.0.0",
		},
	}

	var initResult InitializeResult
	if err := c.transport.call(ctx, "initialize", initParams, &initResult); err != nil {
		_ = c.transport.close()
		return fmt.Errorf("mcp initialize failed: %w", err)
	}

	if err := c.transport.notify(ctx, "notifications/initialized", nil); err != nil {
		_ = c.transport.close()
		return fmt.Errorf("mcp notifications/initialized failed: %w", err)
	}

	return nil
}

func (c *Client) Call(ctx context.Context, method string, params any, result interface{}) error {
	return c.transport.call(ctx, method, params, result)
}

func (c *Client) SendNotification(method string, params any) error {
	return c.transport.notify(context.Background(), method, params)
}

func (c *Client) Close() error {
	return c.transport.close()
}

// newTransport picks the transport implementation for cfg.
func newTransport(name string, cfg ServerConfig) (transport, error) {
	t := strings.ToLower(strings.TrimSpace(cfg.Type))
	switch t {
	case "", "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("mcp server %q: stdio transport requires a command", name)
		}
		return newStdioTransport(name, cfg.Command, cfg.Args, cfg.Env), nil
	case "http", "https":
		return newHTTPTransport(name, cfg.URL, cfg.Headers, cfg.InsecureTLS), nil
	case "sse":
		return newSSETransport(name, cfg.URL, cfg.Headers, cfg.InsecureTLS), nil
	default:
		return nil, fmt.Errorf("mcp server %q: unsupported transport type %q (use stdio, http, or sse)", name, cfg.Type)
	}
}

// idToUint64 normalizes a JSON-RPC id (which may arrive as float64/int64/int
// from JSON decoding) into a uint64 for matching pending requests.
func idToUint64(v any) (uint64, bool) {
	switch x := v.(type) {
	case float64:
		return uint64(x), true
	case int64:
		return uint64(x), true
	case int:
		return uint64(x), true
	case string:
		// numeric strings are also valid ids
		var n uint64
		if _, err := fmt.Sscanf(x, "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}
