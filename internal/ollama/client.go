package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *Client) Version(ctx context.Context) (VersionResponse, error) {
	var out VersionResponse
	err := c.do(ctx, http.MethodGet, "/api/version", nil, &out)
	return out, err
}

func (c *Client) Tags(ctx context.Context) (TagsResponse, error) {
	var out TagsResponse
	err := c.do(ctx, http.MethodGet, "/api/tags", nil, &out)
	return out, err
}

func (c *Client) Ps(ctx context.Context) (PsResponse, error) {
	var out PsResponse
	err := c.do(ctx, http.MethodGet, "/api/ps", nil, &out)
	return out, err
}

func (c *Client) Show(ctx context.Context, model string) (ShowResponse, error) {
	var out ShowResponse
	err := c.do(ctx, http.MethodPost, "/api/show", map[string]string{"model": model}, &out)
	return out, err
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	req.Stream = boolPtr(false)
	var out ChatResponse
	err := c.do(ctx, http.MethodPost, "/api/chat", req, &out)
	return out, err
}

func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onChunk func(ChatResponse) error) error {
	req.Stream = boolPtr(true)
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama POST /api/chat failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk ChatResponse
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := onChunk(chunk); err != nil {
			return err
		}
		if chunk.Done {
			return nil
		}
	}
}

func (c *Client) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	var out EmbedResponse
	err := c.do(ctx, http.MethodPost, "/api/embed", req, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama %s %s failed: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(payload, out)
}

func boolPtr(value bool) *bool {
	return &value
}
