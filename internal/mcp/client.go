package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	name    string
	command string
	args    []string
	env     []string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	reqID     uint64
	pending   map[uint64]chan *JSONRPCResponse
	pendingMu sync.Mutex
	writeMu   sync.Mutex

	done chan struct{}
}

func NewClient(name string, command string, args []string, env map[string]string) *Client {
	var envList []string
	if len(env) > 0 {
		envList = os.Environ()
		for k, v := range env {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return &Client{
		name:    name,
		command: command,
		args:    args,
		env:     envList,
		pending: make(map[uint64]chan *JSONRPCResponse),
		done:    make(chan struct{}),
	}
}

func (c *Client) Start(ctx context.Context) error {
	c.cmd = exec.CommandContext(ctx, c.command, c.args...)
	if len(c.env) > 0 {
		c.cmd.Env = c.env
	}

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	c.stdout = stdout

	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mcp server %s: %w", c.name, err)
	}

	go c.readLoop()
	go c.stderrLoop(stderr)

	// Perform initialize handshake
	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "ollamabot",
			Version: "1.0.0",
		},
	}

	var initResult InitializeResult
	if err := c.Call(ctx, "initialize", initParams, &initResult); err != nil {
		c.Close()
		return fmt.Errorf("mcp initialize failed: %w", err)
	}

	// Send initialized notification
	if err := c.SendNotification("notifications/initialized", nil); err != nil {
		c.Close()
		return fmt.Errorf("mcp notifications/initialized failed: %w", err)
	}

	return nil
}

func (c *Client) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		select {
		case <-c.done:
			return
		default:
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Printf("[%s mcp-client] error parsing response: %v line: %s", c.name, err, string(line))
			continue
		}

		if resp.ID != nil {
			var idVal uint64
			switch v := resp.ID.(type) {
			case float64:
				idVal = uint64(v)
			case int64:
				idVal = uint64(v)
			case int:
				idVal = uint64(v)
			default:
				continue
			}

			c.pendingMu.Lock()
			ch, ok := c.pending[idVal]
			if ok {
				delete(c.pending, idVal)
			}
			c.pendingMu.Unlock()

			if ok {
				ch <- &resp
			}
		}
	}
}

func (c *Client) stderrLoop(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("[mcp-server:%s] %s", c.name, scanner.Text())
	}
}

func (c *Client) Call(ctx context.Context, method string, params any, result interface{}) error {
	id := atomic.AddUint64(&c.reqID, 1)

	var paramsRaw json.RawMessage
	if params != nil {
		pBytes, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
		paramsRaw = pBytes
	}

	req := JSONRPCRequest{
		Jsonrpc: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ch := make(chan *JSONRPCResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	c.writeMu.Lock()
	_, err = c.stdin.Write(append(reqBytes, '\n'))
	c.writeMu.Unlock()

	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return fmt.Errorf("failed to write to stdin: %w", err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}
		return nil
	}
}

func (c *Client) SendNotification(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		pBytes, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
		paramsRaw = pBytes
	}

	req := JSONRPCRequest{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  paramsRaw,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(reqBytes, '\n'))
	if err != nil {
		return fmt.Errorf("failed to write notification: %w", err)
	}
	return nil
}

func (c *Client) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	select {
	case <-c.done:
		return nil
	default:
		close(c.done)
	}

	if c.stdin != nil {
		c.stdin.Close()
	}

	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(os.Interrupt)

		go func() {
			time.Sleep(2 * time.Second)
			if c.cmd.ProcessState == nil || !c.cmd.ProcessState.Exited() {
				_ = c.cmd.Process.Kill()
			}
		}()
		_ = c.cmd.Wait()
	}
	return nil
}
