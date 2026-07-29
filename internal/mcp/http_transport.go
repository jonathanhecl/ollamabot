package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// httpTransport implements the Streamable HTTP transport (MCP 2025-03-26).
// Each request is POSTed to URL; the server replies with either a single
// application/json document or a text/event-stream of responses.
type httpTransport struct {
	name        string
	url         string
	headers     map[string]string
	insecureTLS bool
	client      *http.Client
	sessionID   string
	reqID       uint64
	mu          sync.Mutex
}

func newHTTPTransport(name, rawURL string, headers map[string]string, insecureTLS bool) *httpTransport {
	tlsConfig := defaultTLSConfig()
	if insecureTLS {
		tlsConfig.InsecureSkipVerify = true
	}
	return &httpTransport{
		name:        name,
		url:         rawURL,
		headers:     headers,
		insecureTLS: insecureTLS,
		client: &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig, Proxy: http.ProxyFromEnvironment},
		},
	}
}

func (h *httpTransport) start(ctx context.Context) error {
	// Nothing to prepare; the transport is stateless per request. We could
	// optionally send an initialize here, but Client.Start handles that.
	return nil
}

func (h *httpTransport) nextID() uint64 {
	return atomic.AddUint64(&h.reqID, 1)
}

func (h *httpTransport) call(ctx context.Context, method string, params any, result interface{}) error {
	id := h.nextID()

	paramsRaw, err := marshalParams(params)
	if err != nil {
		return err
	}

	req := JSONRPCRequest{
		Jsonrpc: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	}

	resp, err := h.post(ctx, req, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Capture / refresh session id if the server advertises one.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		h.mu.Lock()
		h.sessionID = sid
		h.mu.Unlock()
	}

	jsonResp, err := readRPCResponse(ctx, resp.Body, id)
	if err != nil {
		return err
	}
	return decodeResponse(jsonResp, result)
}

func (h *httpTransport) notify(ctx context.Context, method string, params any) error {
	paramsRaw, err := marshalParams(params)
	if err != nil {
		return err
	}
	req := JSONRPCRequest{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  paramsRaw,
	}
	resp, err := h.post(ctx, req, false)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (h *httpTransport) close() error { return nil }

// post sends a JSON-RPC request to the configured URL.
func (h *httpTransport) post(ctx context.Context, req JSONRPCRequest, expectResponse bool) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	accept := "application/json, text/event-stream"
	if !expectResponse {
		accept = "application/json"
	}
	httpReq.Header.Set("Accept", accept)
	for k, v := range h.headers {
		httpReq.Header.Set(k, v)
	}
	h.mu.Lock()
	sid := h.sessionID
	h.mu.Unlock()
	if sid != "" {
		httpReq.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp http request to %s failed: %w", h.url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("mcp http request to %s returned status %s", h.url, resp.Status)
	}
	return resp, nil
}

// sseTransport implements the legacy HTTP+SSE transport (MCP 2024-11-05).
// A long-lived SSE stream is opened to URL; the server sends an `endpoint`
// event with a URI to POST requests to, and `message` events carry JSON-RPC
// responses which are routed to waiting callers by id.
type sseTransport struct {
	name        string
	url         string
	headers     map[string]string
	insecureTLS bool
	client      *http.Client
	endpoint    string
	reqID       uint64
	pending     map[uint64]chan *JSONRPCResponse
	pendingMu   sync.Mutex
	body        io.ReadCloser
	cancel      context.CancelFunc
	done        chan struct{}
	startOnce   sync.Once
	startErr    error
	endpointCh  chan struct{}
}

func newSSETransport(name, rawURL string, headers map[string]string, insecureTLS bool) *sseTransport {
	tlsConfig := defaultTLSConfig()
	if insecureTLS {
		tlsConfig.InsecureSkipVerify = true
	}
	return &sseTransport{
		name:        name,
		url:         rawURL,
		headers:     headers,
		insecureTLS: insecureTLS,
		client: &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig, Proxy: http.ProxyFromEnvironment},
		},
		pending:    make(map[uint64]chan *JSONRPCResponse),
		done:       make(chan struct{}),
		endpointCh: make(chan struct{}),
	}
}

func (s *sseTransport) start(ctx context.Context) error {
	sctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	httpReq, err := http.NewRequestWithContext(sctx, http.MethodGet, s.url, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to build sse request: %w", err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range s.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp sse connect to %s failed: %w", s.url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("mcp sse connect to %s returned status %s", s.url, resp.Status)
	}
	s.body = resp.Body

	go s.readLoop()

	// Wait until the server advertises its POST endpoint.
	select {
	case <-s.endpointCh:
		return nil
	case <-sctx.Done():
		return fmt.Errorf("mcp sse: closed before endpoint was received")
	case <-time.After(15 * time.Second):
		return fmt.Errorf("mcp sse: timed out waiting for endpoint event")
	}
}

func (s *sseTransport) readLoop() {
	reader := bufio.NewReader(s.body)
	var eventName string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// End of event: dispatch any accumulated data.
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment / heartbeat
		}
		field, value, ok := splitSSEField(line)
		if !ok {
			continue
		}
		switch field {
		case "event":
			eventName = value
		case "data":
			s.handleSSEData(eventName, value)
		}
	}
}

func (s *sseTransport) handleSSEData(event, data string) {
	switch event {
	case "endpoint":
		// Resolve relative endpoint URLs against the base SSE URL.
		base, err := url.Parse(s.url)
		if err == nil {
			if ref, err := base.Parse(data); err == nil {
				s.endpoint = ref.String()
			} else {
				s.endpoint = data
			}
		} else {
			s.endpoint = data
		}
		s.startOnce.Do(func() { close(s.endpointCh) })
	case "", "message":
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			return
		}
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
			ch <- &resp
		}
	}
}

func (s *sseTransport) call(ctx context.Context, method string, params any, result interface{}) error {
	id := atomic.AddUint64(&s.reqID, 1)
	paramsRaw, err := marshalParams(params)
	if err != nil {
		return err
	}
	req := JSONRPCRequest{
		Jsonrpc: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	}

	ch := make(chan *JSONRPCResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	if err := s.postRequest(ctx, req); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return err
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

func (s *sseTransport) notify(ctx context.Context, method string, params any) error {
	paramsRaw, err := marshalParams(params)
	if err != nil {
		return err
	}
	req := JSONRPCRequest{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  paramsRaw,
	}
	return s.postRequest(ctx, req)
}

func (s *sseTransport) postRequest(ctx context.Context, req JSONRPCRequest) error {
	if s.endpoint == "" {
		return fmt.Errorf("mcp sse: server endpoint not yet known")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("mcp sse post failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp sse post returned status %s", resp.Status)
	}
	return nil
}

func (s *sseTransport) close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.body != nil {
		_ = s.body.Close()
	}
	return nil
}

// --- helpers ---

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}
	return b, nil
}

// readRPCResponse reads a server reply that may be either a single JSON
// document (application/json) or an SSE stream (text/event-stream), and
// returns the JSON-RPC response whose id matches wantID.
func readRPCResponse(ctx context.Context, body io.Reader, wantID uint64) (*JSONRPCResponse, error) {
	// Buffer enough to peek at the content type via the first non-whitespace
	// character. SSE data lines start with "data:", JSON starts with "{".
	br := bufio.NewReader(body)
	first, err := br.Peek(1)
	if err != nil {
		return nil, fmt.Errorf("failed to read mcp response: %w", err)
	}

	if first[0] == '{' {
		// Single JSON document.
		dec := json.NewDecoder(br)
		for {
			var resp JSONRPCResponse
			if err := dec.Decode(&resp); err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("failed to decode mcp response: %w", err)
			}
			if resp.ID == nil {
				continue
			}
			if id, ok := idToUint64(resp.ID); ok && id == wantID {
				return &resp, nil
			}
		}
		return nil, fmt.Errorf("mcp response did not contain id %d", wantID)
	}

	// Treat as SSE stream.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("mcp sse stream ended without response for id %d", wantID)
			}
			return nil, fmt.Errorf("failed to read sse stream: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := splitSSEField(line)
		if !ok {
			continue
		}
		if field != "data" {
			continue
		}
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(value), &resp); err != nil {
			continue
		}
		if resp.ID == nil {
			continue
		}
		if id, ok := idToUint64(resp.ID); ok && id == wantID {
			return &resp, nil
		}
	}
}

// splitSSEField splits an SSE line "field: value" (the leading space after the
// colon is stripped per the SSE spec).
func splitSSEField(line string) (field, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	field = line[:idx]
	value = line[idx+1:]
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value, true
}

// defaultTLSConfig returns a fresh TLS config. If this is Windows, we use the
// system certificate store via crypto/x509; otherwise Go's defaults apply.
func defaultTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}
