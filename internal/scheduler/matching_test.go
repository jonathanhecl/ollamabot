package scheduler

import (
	"testing"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

func TestRelatedTasksUsesEmbeddings(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	btc, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web", Type: "agent_task", Title: "Bitcoin price",
		Prompt: "Check the bitcoin price every 2h", When: "every 2h",
	})
	if err != nil {
		t.Fatal(err)
	}
	news, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web", Type: "agent_task", Title: "Football news",
		Prompt: "Check football news every morning", When: "0 9 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}

	mgr.mu.Lock()
	mgr.embeddings[btc.ID] = taskEmbedding{Vector: []float64{1, 0}, UpdatedAt: time.Now()}
	mgr.embeddings[news.ID] = taskEmbedding{Vector: []float64{0, 1}, UpdatedAt: time.Now()}
	mgr.mu.Unlock()

	related := mgr.RelatedTasks("how is bitcoin doing?", []float64{1, 0}, 3)
	if len(related) != 1 || related[0].ID != btc.ID {
		t.Fatalf("unexpected related tasks: %+v", related)
	}
}

func TestRelatedTasksKeywordFallback(t *testing.T) {
	mgr := newTestManager(t, config.Config{ServerEnabled: true})

	btc, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web", Type: "agent_task", Title: "Bitcoin price",
		Prompt: "Check the bitcoin price every 2h", When: "every 2h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateScheduledTask(tools.ScheduledTaskSpec{
		Channel: "web", Type: "agent_task", Title: "Football news",
		Prompt: "Check football news every morning", When: "0 9 * * *",
	}); err != nil {
		t.Fatal(err)
	}

	related := mgr.RelatedTasks("how is bitcoin doing?", nil, 3)
	if len(related) != 1 || related[0].ID != btc.ID {
		t.Fatalf("unexpected keyword matches: %+v", related)
	}
}
