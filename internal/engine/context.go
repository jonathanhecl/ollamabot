package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func InjectContext(workspace, sessionsPath, sessionID string, messages []ollama.Message) []ollama.Message {
	if strings.TrimSpace(sessionID) == "" {
		return messages
	}
	messages = injectUploadsContext(workspace, sessionsPath, sessionID, messages)
	messages = injectAttachmentsContext(sessionsPath, sessionID, messages)
	return messages
}

func injectUploadsContext(workspace, sessionsPath, sessionID string, messages []ollama.Message) []ollama.Message {
	dir := uploadsDir(workspace, sessionsPath, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return messages
	}

	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		absFile := filepath.Join(dir, e.Name())
		relPath, relErr := filepath.Rel(workspace, absFile)
		if relErr != nil {
			relPath = absFile
		}
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		sizeStr := ""
		if info, err := e.Info(); err == nil {
			sizeStr = fmt.Sprintf(" (%s)", humanSize(info.Size()))
		}
		lines = append(lines, fmt.Sprintf("- %s%s  (path: %s)", e.Name(), sizeStr, relPath))
	}
	if len(lines) == 0 {
		return messages
	}
	note := "The user has uploaded the following files to this session. " +
		"You can read text files with the read_file tool using the given path, " +
		"or run shell commands on binary/video files with execute_command.\n\nUploaded files:\n" +
		strings.Join(lines, "\n")
	return appendSystemNote(messages, note)
}

func injectAttachmentsContext(sessionsPath, sessionID string, messages []ollama.Message) []ollama.Message {
	attDir := filepath.Join(sessionsPath, sessionID, "attachments")
	entries, err := os.ReadDir(attDir)
	if err != nil || len(entries) == 0 {
		return messages
	}

	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if hasExt(e.Name(), ".png", ".jpg", ".jpeg", ".gif", ".webp") {
			kind = "image"
		} else if hasExt(e.Name(), ".wav", ".mp3", ".ogg") {
			kind = "audio"
		} else if strings.HasSuffix(e.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(attDir, e.Name()))
			if err == nil {
				var storage struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				}
				if json.Unmarshal(data, &storage) == nil {
					if storage.Kind != "" {
						kind = storage.Kind
					}
					if storage.Name != "" {
						lines = append(lines, fmt.Sprintf("- %s (kind: %s, size: %s)", storage.Name, kind, humanSize(info.Size())))
						continue
					}
				}
			}
		}
		lines = append(lines, fmt.Sprintf("- %s (kind: %s, size: %s)", e.Name(), kind, humanSize(info.Size())))
	}
	if len(lines) == 0 {
		return messages
	}
	note := "The current session contains the following attachments. " +
		"You can inspect them with the list_session_attachments and view_session_attachment tools. " +
		"For images, you can use view_session_attachment to get the base64 data and then send it to a vision model if needed.\n\n" +
		"Session attachments:\n" + strings.Join(lines, "\n")
	return appendSystemNote(messages, note)
}

func appendSystemNote(messages []ollama.Message, note string) []ollama.Message {
	for i, msg := range messages {
		if msg.Role == "system" {
			messages[i].Content = messages[i].Content + "\n\n" + note
			return messages
		}
	}
	return append([]ollama.Message{{Role: "system", Content: note}}, messages...)
}

func hasExt(name string, exts ...string) bool {
	lower := strings.ToLower(name)
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
