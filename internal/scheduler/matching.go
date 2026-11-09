package scheduler

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// RelatedTasks returns the active tasks most related to the query. It uses the
// stored task embeddings when they match the current model and falls back to
// keyword matching otherwise.
func (m *Manager) RelatedTasks(query string, queryEmbedding []float64, limit int) []tools.ScheduledTaskInfo {
	if limit <= 0 {
		limit = 3
	}
	model := config.ResolveModel(m.config(), config.ModelRoleEmbed)

	m.mu.RLock()
	tasks := make([]Task, 0, len(m.tasks))
	embeddings := make(map[string]taskEmbedding, len(m.embeddings))
	for id, task := range m.tasks {
		if task.Status == TaskStatusCompleted || task.Status == TaskStatusCancelled {
			continue
		}
		tasks = append(tasks, task)
		if emb, ok := m.embeddings[id]; ok {
			embeddings[id] = emb
		}
	}
	m.mu.RUnlock()

	type candidate struct {
		info    tools.ScheduledTaskInfo
		score   float64
		lastRun time.Time
	}
	candidates := make([]candidate, 0, len(tasks))
	for _, task := range tasks {
		score := 0.0
		emb, hasEmb := embeddings[task.ID]
		if len(queryEmbedding) > 0 && hasEmb && emb.Model == model && len(emb.Vector) == len(queryEmbedding) {
			score = cosineSimilarity(queryEmbedding, emb.Vector)
			if score < 0.50 {
				continue
			}
		} else {
			score = keywordTaskScore(query, task)
			if score < 0.30 {
				continue
			}
		}
		last := time.Time{}
		if task.LastRunAt != nil {
			last = *task.LastRunAt
		}
		candidates = append(candidates, candidate{info: task.ToInfo(), score: score, lastRun: last})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].lastRun.After(candidates[j].lastRun)
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	out := make([]tools.ScheduledTaskInfo, len(candidates))
	for i, c := range candidates {
		out[i] = c.info
	}
	return out
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

var taskStopwords = map[string]bool{
	"para": true, "como": true, "hacer": true, "sobre": true, "esta": true, "este": true,
	"esto": true, "con": true, "que": true, "del": true, "los": true, "las": true, "una": true,
	"uno": true, "unas": true, "unos": true, "por": true, "donde": true, "quien": true,
	"cual": true, "cuando": true, "cuanto": true, "the": true, "and": true, "for": true,
	"with": true, "this": true, "that": true, "from": true, "about": true, "what": true,
	"how": true, "hola": true, "buenas": true, "please": true, "help": true, "ayuda": true,
	"mi": true, "mis": true, "tu": true, "tus": true, "el": true, "la": true, "lo": true,
}

func taskTokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		if len([]rune(f)) < 3 || taskStopwords[f] {
			continue
		}
		tokens = append(tokens, f)
	}
	return tokens
}

func keywordTaskScore(query string, task Task) float64 {
	queryTokens := taskTokens(query)
	if len(queryTokens) == 0 {
		return 0
	}
	text := strings.ToLower(deriveTaskTitle(task.Title, task.Prompt) + " " + task.Prompt)
	matched := 0
	for _, token := range queryTokens {
		if strings.Contains(text, token) {
			matched++
		}
	}
	return float64(matched) / float64(len(queryTokens))
}
