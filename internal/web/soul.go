package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/agent"
)

// maxSoulBytes caps the personality file size accepted from the web editor.
const maxSoulBytes = 256 * 1024

type soulResponse struct {
	Content   string `json:"content"`
	Path      string `json:"path"`
	UpdatedAt string `json:"updated_at,omitempty"`
	IsDefault bool   `json:"is_default"`
}

// soulPayload builds the API representation for a soul content. The path and
// timestamp come from disk so the UI can show where the file lives.
func soulPayload(content string) soulResponse {
	resp := soulResponse{
		Content:   content,
		Path:      "agent/SOUL.md",
		IsDefault: agent.IsDefaultSoul(content),
	}
	if path, err := agent.SoulPath(); err == nil {
		resp.Path = path
		if info, err := os.Stat(path); err == nil {
			resp.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	return resp
}

func (s *Server) handleGetSoul(w http.ResponseWriter, r *http.Request) {
	content, err := agent.LoadSoul()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, soulPayload(content))
}

type soulUpdateRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleUpdateSoul(w http.ResponseWriter, r *http.Request) {
	var req soulUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSoulBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("soul content cannot be empty"))
		return
	}
	if err := agent.SaveSoul(req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	content, err := agent.LoadSoul()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, soulPayload(content))
}

func (s *Server) handleResetSoul(w http.ResponseWriter, r *http.Request) {
	if err := agent.ResetSoul(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	content, err := agent.LoadSoul()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, soulPayload(content))
}
