package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// taskEmbedding keeps the vector for a task definition, separate from the task
// records so API responses stay small and readable.
type taskEmbedding struct {
	Model     string    `json:"model"`
	Vector    []float64 `json:"vector"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (m *Manager) loadEmbeddingsLocked() {
	m.embeddings = make(map[string]taskEmbedding)
	if m.embeddingsPath == "" {
		return
	}
	data, err := os.ReadFile(m.embeddingsPath)
	if err != nil {
		return
	}
	var stored map[string]taskEmbedding
	if err := json.Unmarshal(data, &stored); err != nil {
		log.Printf("[scheduler] Failed to read task embeddings: %v", err)
		return
	}
	for id, emb := range stored {
		if _, ok := m.tasks[id]; !ok || len(emb.Vector) == 0 {
			continue
		}
		m.embeddings[id] = emb
	}
}

// saveEmbeddingsLocked persists the embedding map. Callers must hold m.mu.
func (m *Manager) saveEmbeddingsLocked() error {
	if m.embeddingsPath == "" {
		return nil
	}
	data, err := json.Marshal(m.embeddings)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.embeddingsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".scheduler-embeddings-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, m.embeddingsPath)
}

// embedTask computes the vector for one task with the current embedding model.
func (m *Manager) embedTask(id string) {
	model := config.ResolveModel(m.config(), config.ModelRoleEmbed)
	if model == "" || m.client == nil {
		return
	}
	m.mu.RLock()
	task, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok || task.Type != TaskTypeAgentTask {
		return
	}
	m.embedTaskWithModel(id, task, model)
}

func (m *Manager) embedTaskWithModel(id string, task Task, model string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := m.client.Embed(ctx, ollama.EmbedRequest{
		Model: model,
		Input: taskEmbeddingText(task),
	})
	if err != nil || len(resp.Embeddings) == 0 {
		log.Printf("[scheduler] Failed to embed task %s: %v", id, err)
		return
	}

	m.mu.Lock()
	m.embeddings[id] = taskEmbedding{
		Model:     model,
		Vector:    resp.Embeddings[0],
		UpdatedAt: time.Now(),
	}
	err = m.saveEmbeddingsLocked()
	m.mu.Unlock()
	if err != nil {
		log.Printf("[scheduler] Failed to persist embedding for task %s: %v", id, err)
	}
}

func taskEmbeddingText(task Task) string {
	title := deriveTaskTitle(task.Title, task.Prompt)
	return fmt.Sprintf("Scheduled task: %s. %s", title, strings.TrimSpace(task.Prompt))
}

// RefreshEmbeddings recomputes task vectors in the background. Call it when
// the embedding model changes; startup uses the same path.
func (m *Manager) RefreshEmbeddings() {
	go m.refreshTaskEmbeddings(false)
}

func (m *Manager) refreshTaskEmbeddings(force bool) {
	m.refreshMu.Lock()
	if m.refreshing {
		m.refreshMu.Unlock()
		return
	}
	m.refreshing = true
	m.refreshMu.Unlock()
	defer func() {
		m.refreshMu.Lock()
		m.refreshing = false
		m.refreshMu.Unlock()
	}()

	model := config.ResolveModel(m.config(), config.ModelRoleEmbed)
	if model == "" || m.client == nil {
		return
	}

	m.mu.RLock()
	ids := make([]string, 0, len(m.tasks))
	for id, task := range m.tasks {
		if task.Type == TaskTypeAgentTask {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()

	updated := 0
	for _, id := range ids {
		m.mu.RLock()
		task, ok := m.tasks[id]
		emb, hasEmb := m.embeddings[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		if !force && hasEmb && emb.Model == model && len(emb.Vector) > 0 {
			continue
		}
		m.embedTaskWithModel(id, task, model)
		updated++
	}
	if updated > 0 {
		log.Printf("[scheduler] Recomputed %d task embedding(s) with model %q", updated, model)
	}
}
