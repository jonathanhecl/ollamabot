package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client struct {
	mu         sync.RWMutex
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// No global timeout on the client — streaming chat and multi-turn
		// tool loops can run for minutes. Timeouts are enforced via
		// context.WithTimeout at the call sites where appropriate.
		httpClient: &http.Client{},
	}
}

func (c *Client) SetBaseURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseURL = strings.TrimRight(url, "/")
}

func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
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

	maxRetries := 3
	backoff := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL()+"/api/chat", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return err
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if (resp.StatusCode >= 500 || resp.StatusCode == 429) && attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return fmt.Errorf("ollama POST /api/chat failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		decoder := json.NewDecoder(resp.Body)
		decodedCount := 0
		var streamErr error
		for {
			var chunk ChatResponse
			if err := decoder.Decode(&chunk); err != nil {
				if err == io.EOF {
					resp.Body.Close()
					return nil
				}
				streamErr = err
				break
			}
			decodedCount++
			if err := onChunk(chunk); err != nil {
				resp.Body.Close()
				return err
			}
			if chunk.Done {
				resp.Body.Close()
				return nil
			}
		}
		resp.Body.Close()

		if streamErr != nil {
			if decodedCount == 0 && attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return streamErr
		}
	}
	return fmt.Errorf("max retries exceeded")
}

func (c *Client) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	var out EmbedResponse
	err := c.do(ctx, http.MethodPost, "/api/embed", req, &out)
	return out, err
}

// Generate calls /api/generate for image generation models (non-streaming)
func (c *Client) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	req.Stream = boolPtr(false)
	var out GenerateResponse
	err := c.do(ctx, http.MethodPost, "/api/generate", req, &out)
	return out, err
}

// GenerateStream calls /api/generate with streaming for image generation models
func (c *Client) GenerateStream(ctx context.Context, req GenerateRequest, onChunk func(GenerateResponse) error) error {
	req.Stream = boolPtr(true)
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	maxRetries := 3
	backoff := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL()+"/api/generate", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return err
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if (resp.StatusCode >= 500 || resp.StatusCode == 429) && attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return fmt.Errorf("ollama POST /api/generate failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		decoder := json.NewDecoder(resp.Body)
		decodedCount := 0
		var streamErr error
		for {
			var chunk GenerateResponse
			if err := decoder.Decode(&chunk); err != nil {
				if err == io.EOF {
					resp.Body.Close()
					return nil
				}
				streamErr = err
				break
			}
			decodedCount++
			if err := onChunk(chunk); err != nil {
				resp.Body.Close()
				return err
			}
			if chunk.Done {
				resp.Body.Close()
				return nil
			}
		}
		resp.Body.Close()

		if streamErr != nil {
			if decodedCount == 0 && attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return streamErr
		}
	}
	return fmt.Errorf("max retries exceeded")
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	maxRetries := 3
	backoff := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if body != nil {
			reader = bytes.NewReader(payload)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL()+path, reader)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return err
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			if attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return err
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if (resp.StatusCode >= 500 || resp.StatusCode == 429) && attempt < maxRetries-1 && ctx.Err() == nil {
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return fmt.Errorf("ollama %s %s failed: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		if out == nil {
			return nil
		}
		return json.Unmarshal(respBody, out)
	}
	return fmt.Errorf("max retries exceeded")
}

func boolPtr(value bool) *bool {
	return &value
}
