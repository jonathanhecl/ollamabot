package scheduler

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

func TestTaskEmbeddingsPersist(t *testing.T) {
	cfg := config.Config{
		SessionsPath:  filepath.Join(t.TempDir(), "sessions"),
		ServerEnabled: true,
	}
	mgr := newTestManager(t, cfg)
	task, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web", Type: "agent_task", Title: "Bitcoin price",
		Prompt: "Check the bitcoin price every 2h", When: "every 2h",
	})
	if err != nil {
		t.Fatal(err)
	}

	mgr.mu.Lock()
	mgr.embeddings[task.ID] = taskEmbedding{
		Model:     "embed-model",
		Vector:    []float64{0.5, 0.5},
		UpdatedAt: time.Now(),
	}
	if err := mgr.saveEmbeddingsLocked(); err != nil {
		mgr.mu.Unlock()
		t.Fatal(err)
	}
	mgr.mu.Unlock()

	reloaded := newTestManager(t, cfg)
	reloaded.mu.RLock()
	emb, ok := reloaded.embeddings[task.ID]
	reloaded.mu.RUnlock()
	if !ok || len(emb.Vector) != 2 || emb.Model != "embed-model" {
		t.Fatalf("embedding not reloaded: %+v", emb)
	}
}

func TestDeleteScheduledTaskRemovesEmbedding(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})
	task, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web", Type: "agent_task", Title: "Bitcoin price",
		Prompt: "Check the bitcoin price", When: "every 2h",
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	mgr.embeddings[task.ID] = taskEmbedding{Vector: []float64{1}}
	mgr.mu.Unlock()

	if err := mgr.DeleteScheduledTask(task.ID); err != nil {
		t.Fatal(err)
	}
	mgr.mu.RLock()
	_, ok := mgr.embeddings[task.ID]
	mgr.mu.RUnlock()
	if ok {
		t.Fatal("expected the embedding to be removed with the task")
	}
}
