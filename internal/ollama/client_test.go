package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
