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

// stdioTransport launches a local subprocess and exchanges newline-delimited
// JSON-RPC messages over its stdin/stdout (the classic MCP stdio transport).
type stdioTransport struct {
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

func newStdioTransport(name, command string, args []string, env map[string]string) *stdioTransport {
	var envList []string
	if len(env) > 0 {
		envList = os.Environ()
		for k, v := range env {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return &stdioTransport{
		name:    name,
		command: command,
		args:    args,
		env:     envList,
		pending: make(map[uint64]chan *JSONRPCResponse),
		done:    make(chan struct{}),
	}
}

func (s *stdioTransport) start(ctx context.Context) error {
	s.cmd = exec.CommandContext(ctx, s.command, s.args...)
	if len(s.env) > 0 {
		s.cmd.Env = s.env
	}

	stdin, err := s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	s.stdin = stdin

	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	s.stdout = stdout

	stderr, err := s.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mcp server %s: %w", s.name, err)
	}

	go s.readLoop()
	go s.stderrLoop(stderr)
	return nil
}

func (s *stdioTransport) readLoop() {
	defer failPending(&s.pendingMu, s.pending, "mcp stdio: server process closed the connection")
	reader := bufio.NewReader(s.stdout)
	for {
		select {
		case <-s.done:
			return
		default:
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Printf("[%s mcp-client] error parsing response: %v line: %s", s.name, err, string(line))
			continue
		}

		s.dispatchResponse(&resp)
	}
}

// dispatchResponse routes a response to the waiting caller, if any.
func (s *stdioTransport) dispatchResponse(resp *JSONRPCResponse) {
	if resp.ID == nil {
		return
	}
	idVal, ok := idToUint64(resp.ID)
	if !ok {
		return
	}

	s.pendingMu.Lock()
	ch, exists := s.pending[idVal]
	if exists {
		delete(s.pending, idVal)
	}
	s.pendingMu.Unlock()

	if exists {
		ch <- resp
	}
}

func (s *stdioTransport) stderrLoop(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("[mcp-server:%s] %s", s.name, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[mcp-server:%s] stderr scanner error: %v", s.name, err)
	}
}

func (s *stdioTransport) call(ctx context.Context, method string, params any, result interface{}) error {
	id := atomic.AddUint64(&s.reqID, 1)

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
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	s.writeMu.Lock()
	_, err = s.stdin.Write(append(reqBytes, '\n'))
	s.writeMu.Unlock()

	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return fmt.Errorf("failed to write to stdin: %w", err)
	}

	select {
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return ctx.Err()
	case resp := <-ch:
		return decodeResponse(resp, result)
	}
}

func (s *stdioTransport) protocolVersion() string { return protocolVersionLegacy }

func (s *stdioTransport) notify(ctx context.Context, method string, params any) error {
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

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(reqBytes, '\n'))
	if err != nil {
		return fmt.Errorf("failed to write notification: %w", err)
	}
	return nil
}

func (s *stdioTransport) close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	select {
	case <-s.done:
		return nil
	default:
		close(s.done)
	}

	if s.stdin != nil {
		s.stdin.Close()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)

		go func() {
			time.Sleep(2 * time.Second)
			if s.cmd.ProcessState == nil || !s.cmd.ProcessState.Exited() {
				_ = s.cmd.Process.Kill()
			}
		}()
		_ = s.cmd.Wait()
	}
	return nil
}

// decodeResponse unmarshals a JSON-RPC response into result or returns its error.
func decodeResponse(resp *JSONRPCResponse, result interface{}) error {
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
