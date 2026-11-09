package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempWorkingDir switches to an empty temp dir so the soul helpers never
// touch the repository's runtime agent folder.
func withTempWorkingDir(t *testing.T) {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})
}

func TestSoulAPI(t *testing.T) {
	withTempWorkingDir(t)
	s := &Server{}

	t.Run("get creates default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/soul", nil)
		w := httptest.NewRecorder()
		s.handleGetSoul(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("get status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		var resp soulResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if !resp.IsDefault {
			t.Fatalf("expected default soul on first read")
		}
		if !strings.Contains(resp.Content, "Core Truths") {
			t.Fatalf("default soul missing expected content: %q", resp.Content)
		}
		if resp.Path != filepath.Join("agent", "SOUL.md") {
			t.Fatalf("unexpected path %q", resp.Path)
		}
	})

	t.Run("update persists content", func(t *testing.T) {
		custom := "# Identity\n\n- Name: Nova\n- Tone: dry and concise\n"
		body, _ := json.Marshal(soulUpdateRequest{Content: custom})
		req := httptest.NewRequest(http.MethodPut, "/api/soul", bytes.NewReader(body))
		w := httptest.NewRecorder()
		s.handleUpdateSoul(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("update status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		var resp soulResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Content != custom || resp.IsDefault {
			t.Fatalf("unexpected payload: %+v", resp)
		}

		onDisk, err := os.ReadFile(filepath.Join("agent", "SOUL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(onDisk) != custom {
			t.Fatalf("disk content = %q", onDisk)
		}
		if _, err := os.Stat(filepath.Join("agent", "backups", "SOUL.md.bak1")); err != nil {
			t.Fatalf("expected a backup of the previous soul: %v", err)
		}
	})

	t.Run("rejects empty content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/soul", strings.NewReader(`{"content":"   "}`))
		w := httptest.NewRecorder()
		s.handleUpdateSoul(w, req)
		if w.Result().StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Result().StatusCode)
		}
	})

	t.Run("reset restores default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/soul/reset", nil)
		w := httptest.NewRecorder()
		s.handleResetSoul(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("reset status = %d body=%s", w.Result().StatusCode, w.Body.String())
		}
		var resp soulResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if !resp.IsDefault {
			t.Fatalf("expected default soul after reset")
		}
	})
}
