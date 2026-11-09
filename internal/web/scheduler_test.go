package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/scheduler"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

func TestSchedulerTasksAPI(t *testing.T) {
	tmp := t.TempDir()
	cfgMgr := config.NewManager(config.Config{
		SessionsPath:  filepath.Join(tmp, "sessions"),
		MemoryPath:    filepath.Join(tmp, "memory"),
		ServerEnabled: true,
	})
	s := &Server{
		cfgMgr:       cfgMgr,
		sessionStore: sessions.NewStore(cfgMgr.Get().SessionsPath),
		schedulerMgr: scheduler.NewManager(cfgMgr, nil, nil, nil),
	}

	var created struct {
		ID             string   `json:"id"`
		Title          string   `json:"title"`
		SessionID      string   `json:"session_id"`
		NotifyChannels []string `json:"notify_channels"`
		NotifyMode     string   `json:"notify_mode"`
	}

	t.Run("create", func(t *testing.T) {
		body := `{"text":"Tell me how bitcoin is doing","title":"Bitcoin check","when":"0 9 * * *","is_agent_task":true,"notify_channels":["web","telegram"],"notify_mode":"on_alert"}`
		req := httptest.NewRequest(http.MethodPost, "/api/scheduler/tasks", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleCreateSchedulerTask(w, req)
		if w.Result().StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if created.Title != "Bitcoin check" {
			t.Fatalf("title = %q", created.Title)
		}
		if len(created.NotifyChannels) != 2 {
			t.Fatalf("channels = %v", created.NotifyChannels)
		}
		if created.SessionID == "" {
			t.Fatal("expected a dedicated session for a web-delivered task")
		}
		if created.NotifyMode != "on_alert" {
			t.Fatalf("notify mode = %q, want on_alert", created.NotifyMode)
		}
	})

	t.Run("update", func(t *testing.T) {
		body := `{"title":"BTC morning","status":"paused","notify_mode":"always"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/scheduler/tasks/"+created.ID, strings.NewReader(body))
		req.SetPathValue("id", created.ID)
		w := httptest.NewRecorder()
		s.handleUpdateSchedulerTask(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("update status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		var updated struct {
			Title      string `json:"title"`
			Status     string `json:"status"`
			NotifyMode string `json:"notify_mode"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
			t.Fatal(err)
		}
		if updated.Title != "BTC morning" || updated.Status != "paused" {
			t.Fatalf("unexpected update: %+v", updated)
		}
		if updated.NotifyMode != "always" {
			t.Fatalf("notify mode = %q, want always", updated.NotifyMode)
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/scheduler/tasks/"+created.ID, nil)
		req.SetPathValue("id", created.ID)
		w := httptest.NewRecorder()
		s.handleCancelSchedulerTask(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("delete status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		if _, err := s.schedulerMgr.GetTask(created.ID); err == nil {
			t.Fatal("expected task to be deleted")
		}
	})
}
