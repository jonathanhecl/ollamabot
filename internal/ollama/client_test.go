package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_ = json.NewEncoder(w).Encode(VersionResponse{Version: "0.24.0"})
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(TagsResponse{Models: []ModelTag{{Name: "qwen3:8b"}}})
		case "/api/ps":
			_ = json.NewEncoder(w).Encode(PsResponse{Models: []RunningModel{{Name: "qwen3:8b", SizeVRAM: 1024}}})
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ShowResponse{Capabilities: []string{"completion", "tools"}})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(ChatResponse{Done: true, Message: Message{Role: "assistant", Content: "ok"}})
		case "/api/embed":
			_ = json.NewEncoder(w).Encode(EmbedResponse{Embeddings: [][]float64{{0.1, 0.2}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()
	if version, err := client.Version(ctx); err != nil || version.Version != "0.24.0" {
		t.Fatalf("version = %#v err=%v", version, err)
	}
	if tags, err := client.Tags(ctx); err != nil || len(tags.Models) != 1 {
		t.Fatalf("tags = %#v err=%v", tags, err)
	}
	if ps, err := client.Ps(ctx); err != nil || len(ps.Models) != 1 || ps.Models[0].SizeVRAM != 1024 {
		t.Fatalf("ps = %#v err=%v", ps, err)
	}
	if show, err := client.Show(ctx, "qwen3:8b"); err != nil || len(show.Capabilities) != 2 {
		t.Fatalf("show = %#v err=%v", show, err)
	}
	if chat, err := client.Chat(ctx, ChatRequest{Model: "qwen3:8b"}); err != nil || chat.Message.Content != "ok" {
		t.Fatalf("chat = %#v err=%v", chat, err)
	}
	if embed, err := client.Embed(ctx, EmbedRequest{Model: "nomic", Input: "hello"}); err != nil || len(embed.Embeddings[0]) != 2 {
		t.Fatalf("embed = %#v err=%v", embed, err)
	}
}

func TestClientStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		if r.URL.Path == "/api/chat" {
			c1 := ChatResponse{Message: Message{Content: "Hello"}, Done: false}
			b1, _ := json.Marshal(c1)
			_, _ = w.Write(append(b1, '\n'))

			c2 := ChatResponse{Message: Message{Content: " world"}, Done: true}
			b2, _ := json.Marshal(c2)
			_, _ = w.Write(append(b2, '\n'))
		} else if r.URL.Path == "/api/generate" {
			g1 := GenerateResponse{Response: "First chunk", Done: false}
			b1, _ := json.Marshal(g1)
			_, _ = w.Write(append(b1, '\n'))

			g2 := GenerateResponse{Response: "Second chunk", Done: true}
			b2, _ := json.Marshal(g2)
			_, _ = w.Write(append(b2, '\n'))
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	// Test ChatStream
	var chatResult []string
	err := client.ChatStream(ctx, ChatRequest{Model: "test"}, func(resp ChatResponse) error {
		chatResult = append(chatResult, resp.Message.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	if strings.Join(chatResult, "") != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", strings.Join(chatResult, ""))
	}

	// Test GenerateStream
	var generateResult []string
	err = client.GenerateStream(ctx, GenerateRequest{Model: "test"}, func(resp GenerateResponse) error {
		generateResult = append(generateResult, resp.Response)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}
	if strings.Join(generateResult, "") != "First chunkSecond chunk" {
		t.Errorf("expected 'First chunkSecond chunk', got %q", strings.Join(generateResult, ""))
	}
}

func TestClientGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := GenerateResponse{
			Response: "Generate output",
			Done:     true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	res, err := client.Generate(context.Background(), GenerateRequest{Model: "test"})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.Response != "Generate output" || !res.Done {
		t.Errorf("unexpected GenerateResponse: %+v", res)
	}
}

func TestClientRetries(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		currentAttempt := atomic.AddInt32(&attempts, 1)
		if currentAttempt < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("temporary failure"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(VersionResponse{Version: "0.24.0"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	
	// Speed up backoff for tests
	// Note: client retry code has:
	// time.Sleep(backoff)
	// backoff *= 2
	// with default backoff of 500ms. 500ms + 1000ms = 1.5s total wait. That's fine for tests.
	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatalf("expected retry to succeed on 3rd attempt, got err: %v", err)
	}
	if version.Version != "0.24.0" {
		t.Errorf("expected version 0.24.0, got %q", version.Version)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected exactly 3 attempts, got %d", attempts)
	}
}

func TestClientRetriesFailure(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Version(context.Background())
	if err == nil {
		t.Fatal("expected client call to fail after max retries, got nil")
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected exactly 3 attempts, got %d", attempts)
	}
}

func TestClientContextCancellationDuringRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	
	// Start a goroutine to cancel the context very quickly
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := client.Version(ctx)
	if err == nil {
		t.Fatal("expected request to fail, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got: %v", err)
	}
}

func TestClientErrorsAndEdgeCases(t *testing.T) {
	// 1. Invalid method or URL in do()
	badClient := NewClient("http://[invalid-url]")
	_, err := badClient.Version(context.Background())
	if err == nil {
		t.Fatal("expected error with invalid URL, got nil")
	}

	// 2. Body marshal failure
	// We can pass a struct that can't be marshaled to JSON.
	// E.g., a map with non-string keys, or a channel, or function.
	type unmarshalable struct {
		Fn func() `json:"fn"`
	}
	client := NewClient("http://localhost:8080")
	_, err = client.Generate(context.Background(), GenerateRequest{
		Options: map[string]any{
			"bad": func() {},
		},
	})
	if err == nil {
		t.Fatal("expected json marshal error, got nil")
	}

	// 3. Bad response body reading
	// We can return a body that errors on Read.
	errBodyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set content length to force reading, but close the connection abruptly
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		// Close the hijacker connection or close body immediately
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer errBodyServer.Close()

	clientErrBody := NewClient(errBodyServer.URL)
	_, err = clientErrBody.Version(context.Background())
	if err == nil {
		t.Fatal("expected error reading body, got nil")
	}

	// 4. Bad JSON from server
	badJsonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer badJsonServer.Close()

	clientBadJson := NewClient(badJsonServer.URL)
	_, err = clientBadJson.Version(context.Background())
	if err == nil {
		t.Fatal("expected json unmarshal error, got nil")
	}
}

func TestClientStreamingErrors(t *testing.T) {
	// Server returns non-2xx code for stream
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error message"))
	}))
	defer failServer.Close()

	client := NewClient(failServer.URL)

	err := client.ChatStream(context.Background(), ChatRequest{Model: "test"}, func(ChatResponse) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected ChatStream to fail on non-2xx, got nil")
	}

	err = client.GenerateStream(context.Background(), GenerateRequest{Model: "test"}, func(GenerateResponse) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected GenerateStream to fail on non-2xx, got nil")
	}

	// Server returns invalid JSON in stream
	badJsonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bad-chunk-1\n"))
	}))
	defer badJsonServer.Close()

	clientBadJson := NewClient(badJsonServer.URL)
	err = clientBadJson.ChatStream(context.Background(), ChatRequest{Model: "test"}, func(ChatResponse) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected ChatStream to fail on bad json chunk, got nil")
	}

	err = clientBadJson.GenerateStream(context.Background(), GenerateRequest{Model: "test"}, func(GenerateResponse) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected GenerateStream to fail on bad json chunk, got nil")
	}
}

func TestClientStreamingAdditionalEdgeCases(t *testing.T) {
	// 1. Callback returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		c1 := ChatResponse{Message: Message{Content: "Hello"}, Done: false}
		b1, _ := json.Marshal(c1)
		_, _ = w.Write(append(b1, '\n'))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	cbErr := errors.New("callback error")
	err := client.ChatStream(context.Background(), ChatRequest{Model: "test"}, func(ChatResponse) error {
		return cbErr
	})
	if !errors.Is(err, cbErr) {
		t.Errorf("expected callback error, got: %v", err)
	}

	err = client.GenerateStream(context.Background(), GenerateRequest{Model: "test"}, func(GenerateResponse) error {
		return cbErr
	})
	if !errors.Is(err, cbErr) {
		t.Errorf("expected callback error, got: %v", err)
	}

	// 2. Retry on chunk decoding error (when decodedCount == 0)
	var attempts int32
	retryDecodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if att == 1 {
			_, _ = w.Write([]byte("bad-json\n"))
			return
		}
		c1 := ChatResponse{Message: Message{Content: "Success"}, Done: true}
		b1, _ := json.Marshal(c1)
		_, _ = w.Write(append(b1, '\n'))
	}))
	defer retryDecodeServer.Close()

	clientRetryDecode := NewClient(retryDecodeServer.URL)
	var chatResult []string
	err = clientRetryDecode.ChatStream(context.Background(), ChatRequest{Model: "test"}, func(resp ChatResponse) error {
		chatResult = append(chatResult, resp.Message.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream with decode retry failed: %v", err)
	}
	if strings.Join(chatResult, "") != "Success" {
		t.Errorf("expected Success, got %q", strings.Join(chatResult, ""))
	}

	// 3. Retry on HTTP 500 in stream
	attempts = 0
	retryHttpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		c1 := GenerateResponse{Response: "SuccessGen", Done: true}
		b1, _ := json.Marshal(c1)
		_, _ = w.Write(append(b1, '\n'))
	}))
	defer retryHttpServer.Close()

	clientRetryHttp := NewClient(retryHttpServer.URL)
	var genResult []string
	err = clientRetryHttp.GenerateStream(context.Background(), GenerateRequest{Model: "test"}, func(resp GenerateResponse) error {
		genResult = append(genResult, resp.Response)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream with HTTP retry failed: %v", err)
	}
	if strings.Join(genResult, "") != "SuccessGen" {
		t.Errorf("expected SuccessGen, got %q", strings.Join(genResult, ""))
	}

	// 4. httpClient.Do fails completely (network error)
	deadClient := NewClient("http://localhost:12345")
	err = deadClient.ChatStream(context.Background(), ChatRequest{Model: "test"}, func(ChatResponse) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for dead port, got nil")
	}

	err = deadClient.GenerateStream(context.Background(), GenerateRequest{Model: "test"}, func(GenerateResponse) error {
		return nil
	})
	if err == nil {
		t.Error("expected error for dead port, got nil")
	}

	// 5. Context cancelled initially
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = client.ChatStream(cancelledCtx, ChatRequest{Model: "test"}, func(ChatResponse) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	err = client.GenerateStream(cancelledCtx, GenerateRequest{Model: "test"}, func(GenerateResponse) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

