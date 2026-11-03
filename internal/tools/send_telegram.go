package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TelegramSenderClient abstracts HTTP requests to Telegram Bot API (useful for testing).
type TelegramSenderClient interface {
	SendMessage(ctx context.Context, apiBase string, chatID int64, text string) error
	SendDocument(ctx context.Context, apiBase string, chatID int64, filePath string, caption string) error
	SendPhoto(ctx context.Context, apiBase string, chatID int64, filePath string, caption string) error
}

type defaultTelegramSender struct {
	client *http.Client
}

func (s *defaultTelegramSender) SendMessage(ctx context.Context, apiBase string, chatID int64, text string) error {
	type reqPayload struct {
		ChatID    int64  `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode,omitempty"`
	}

	sendReq := func(parseMode string) error {
		payload, err := json.Marshal(reqPayload{
			ChatID:    chatID,
			Text:      text,
			ParseMode: parseMode,
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/sendMessage", bytes.NewBuffer(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		var res struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return err
		}
		if !res.OK {
			return fmt.Errorf("telegram API error: %s", res.Description)
		}
		return nil
	}

	// Try with Markdown first; if rejected by Telegram markdown parser, retry plain text
	if err := sendReq("Markdown"); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "can't parse entities") {
			return sendReq("")
		}
		return err
	}
	return nil
}

func (s *defaultTelegramSender) SendPhoto(ctx context.Context, apiBase string, chatID int64, filePath string, caption string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}

	part, err := writer.CreateFormFile("photo", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	_ = writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/sendPhoto", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("telegram API error (sendPhoto): %s", res.Description)
	}
	return nil
}

func (s *defaultTelegramSender) SendDocument(ctx context.Context, apiBase string, chatID int64, filePath string, caption string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}

	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	_ = writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/sendDocument", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("telegram API error (sendDocument): %s", res.Description)
	}
	return nil
}

// SendTelegram executes the dispatch of text and files to Telegram.
func SendTelegram(ctx context.Context, botToken string, targetChatID int64, authorizedIDs []string, message string, paths []string, workspace, sessionsPath, sessionID string, customSender TelegramSenderClient) (string, error) {
	if strings.TrimSpace(botToken) == "" {
		return "", fmt.Errorf("telegram bot token is not configured (TELEGRAM_BOT_TOKEN is empty in settings)")
	}

	// Resolve destination chat ID
	resolvedChatID := targetChatID
	if resolvedChatID == 0 {
		for _, authID := range authorizedIDs {
			trimmed := strings.TrimSpace(authID)
			if trimmed != "" {
				if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil && id != 0 {
					resolvedChatID = id
					break
				}
			}
		}
	}

	if resolvedChatID == 0 {
		return "", fmt.Errorf("no Telegram chat_id provided and no valid authorized account found in TELEGRAM_AUTHORIZED_IDS")
	}

	var sender TelegramSenderClient = customSender
	if sender == nil {
		sender = &defaultTelegramSender{
			client: &http.Client{Timeout: 45 * time.Second},
		}
	}

	apiBase := "https://api.telegram.org/bot" + botToken

	// 1. Send the text message if provided
	trimmedMsg := strings.TrimSpace(message)
	if trimmedMsg != "" {
		if err := sender.SendMessage(ctx, apiBase, resolvedChatID, trimmedMsg); err != nil {
			return "", fmt.Errorf("failed to send message to Telegram: %w", err)
		}
	}

	// 2. Send any file attachments
	sentFiles := 0
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// Try resolving in workspace first
		var resolved string
		var stat os.FileInfo
		var err error

		if workspace != "" {
			resolved, err = ResolveAndValidatePath(workspace, p)
			if err == nil {
				stat, err = os.Stat(resolved)
			}
		}

		// If not found in workspace, check session uploads directory
		if err != nil || stat == nil {
			if sessionsPath != "" && sessionID != "" {
				uploadCandidate := filepath.Join(sessionsPath, sessionID, "uploads", filepath.Base(p))
				if s, e := os.Stat(uploadCandidate); e == nil && !s.IsDir() {
					resolved = uploadCandidate
					stat = s
					err = nil
				}
			}
		}

		if err != nil || stat == nil {
			return "", fmt.Errorf("file not found: %s", p)
		}
		if stat.IsDir() {
			return "", fmt.Errorf("sending directories directly is not supported, please send files: %s", p)
		}

		ext := strings.ToLower(filepath.Ext(resolved))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif":
			if err := sender.SendPhoto(ctx, apiBase, resolvedChatID, resolved, filepath.Base(resolved)); err != nil {
				return "", fmt.Errorf("failed to send photo %s to Telegram: %w", filepath.Base(resolved), err)
			}
		default:
			if err := sender.SendDocument(ctx, apiBase, resolvedChatID, resolved, filepath.Base(resolved)); err != nil {
				return "", fmt.Errorf("failed to send document %s to Telegram: %w", filepath.Base(resolved), err)
			}
		}
		sentFiles++
	}

	if sentFiles > 0 {
		return fmt.Sprintf("Successfully sent message and %d file(s) to Telegram (chat ID: %d).", sentFiles, resolvedChatID), nil
	}
	return fmt.Sprintf("Successfully sent message to Telegram (chat ID: %d).", resolvedChatID), nil
}
