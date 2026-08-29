package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestTelegramOOMRecoveryRetriesThenUsesSubagent(t *testing.T) {
	var models []string
	var notices []string
	unloaded := false
	oom := errors.New("mlx runner failed: Insufficient Memory (00000008:kIOGPUCommandBufferCallbackErrorOutOfMemory)")

	history, usedModel, err := runTelegramWithOOMRecovery(context.Background(), "main:70b", "side:8b", time.Nanosecond, func(message string) {
		notices = append(notices, message)
	}, func() {
		unloaded = true
	}, func(model string) ([]ollama.Message, error) {
		models = append(models, model)
		if model == "main:70b" {
			return nil, oom
		}
		return []ollama.Message{{Role: "assistant", Content: "fallback ok"}}, nil
	})

	if err != nil {
		t.Fatalf("runTelegramWithOOMRecovery: %v", err)
	}
	if usedModel != "side:8b" {
		t.Fatalf("used model = %q, want side:8b", usedModel)
	}
	if len(models) != 3 || models[0] != "main:70b" || models[1] != "main:70b" || models[2] != "side:8b" {
		t.Fatalf("model attempts = %#v", models)
	}
	if len(notices) != 2 {
		t.Fatalf("notices = %#v, want two", notices)
	}
	if !unloaded {
		t.Fatal("expected main model unload before fallback")
	}
	if len(history) != 1 || history[0].Content != "fallback ok" {
		t.Fatalf("history = %#v", history)
	}
}

func TestTelegramOOMRecoveryDoesNotRetryOtherErrors(t *testing.T) {
	attempts := 0
	wantErr := errors.New("connection refused")
	_, usedModel, err := runTelegramWithOOMRecovery(context.Background(), "main", "side", time.Nanosecond, nil, nil, func(model string) ([]ollama.Message, error) {
		attempts++
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if usedModel != "main" {
		t.Fatalf("used model = %q, want main", usedModel)
	}
}

func TestProcessTurnCanBeStoppedBySession(t *testing.T) {
	chatStarted := make(chan struct{})
	releaseChat := make(chan struct{})
	var once sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{Capabilities: []string{"completion"}})
		case "/api/chat":
			once.Do(func() { close(chatStarted) })
			<-releaseChat
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	defer close(releaseChat)

	cfg := config.Config{
		OllamaBaseURL:      ts.URL,
		OllamaDefaultModel: "configured-main",
		Workspace:          t.TempDir(),
		SessionsPath:       t.TempDir(),
		MemoryPath:         t.TempDir(),
		SkillsPath:         t.TempDir(),
	}
	store := sessions.NewStore(cfg.SessionsPath)
	const sessionID = "stop-test"
	if err := store.Save(sessions.Session{ID: sessionID, Title: "Stop test", Model: cfg.OllamaDefaultModel}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ProcessTurn(context.Background(), Deps{
			ConfigMgr:    config.NewManager(cfg),
			Client:       ollama.NewClient(ts.URL),
			SessionStore: store,
		}, TurnRequest{
			SessionID: sessionID,
			Channel:   "telegram",
			Messages:  []router.MediaMessage{{Message: ollama.Message{Role: "user", Content: "wait"}}},
		})
		done <- err
	}()

	select {
	case <-chatStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("chat request did not start")
	}
	if !sessions.AbortSession(sessionID) {
		t.Fatal("expected active session to be aborted")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ProcessTurn error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProcessTurn did not stop after abort")
	}
}

func TestProcessTurnUsesConfiguredMainModel(t *testing.T) {
	var requestedModel string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			var req ollama.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requestedModel = req.Model
			_ = json.NewEncoder(w).Encode(ollama.ChatResponse{
				Message: ollama.Message{Role: "assistant", Content: "ok"},
				Done:    true,
			})
		case "/api/show":
			_ = json.NewEncoder(w).Encode(ollama.ShowResponse{
				Capabilities: []string{"completion", "tools"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	cfg := config.Config{
		OllamaBaseURL:      ts.URL,
		OllamaDefaultModel: "configured-main",
		Workspace:          t.TempDir(),
		SessionsPath:       t.TempDir(),
		MemoryPath:         t.TempDir(),
		SkillsPath:         t.TempDir(),
	}
	client := ollama.NewClient(ts.URL)

	result, err := ProcessTurn(context.Background(), Deps{
		ConfigMgr: config.NewManager(cfg),
		Client:    client,
	}, TurnRequest{
		Channel: "web",
		Messages: []router.MediaMessage{
			{Message: ollama.Message{Role: "user", Content: "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("ProcessTurn failed: %v", err)
	}
	if result.ModelUsed != "configured-main" {
		t.Fatalf("ModelUsed = %q, want configured-main", result.ModelUsed)
	}
	if requestedModel != "configured-main" {
		t.Fatalf("Ollama request model = %q, want configured-main", requestedModel)
	}
}
