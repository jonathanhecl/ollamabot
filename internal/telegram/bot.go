package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"os/exec"
	"runtime"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// Telegram API structures
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from,omitempty"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text,omitempty"`
	Date      int64       `json:"date"`
	Photo     []PhotoSize `json:"photo,omitempty"`
	Voice     *Voice      `json:"voice,omitempty"`
	Audio     *Audio      `json:"audio,omitempty"`
}

type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type Voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type Audio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	FileName     string `json:"file_name,omitempty"`
}

type GetFileResponse struct {
	OK     bool  `json:"ok"`
	Result *File `json:"result,omitempty"`
}

type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

// SessionManager manages the mapping of Telegram chat ID to sessions.
type SessionManager struct {
	mu       sync.RWMutex
	filePath string
	mapping  map[string]string
}

func NewSessionManager(sessionsPath string) *SessionManager {
	return &SessionManager{
		filePath: filepath.Join(sessionsPath, "telegram_sessions.json"),
		mapping:  make(map[string]string),
	}
}

func (sm *SessionManager) Load() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	data, err := os.ReadFile(sm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			sm.mapping = make(map[string]string)
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &sm.mapping)
}

func (sm *SessionManager) Save() error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	data, err := json.MarshalIndent(sm.mapping, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sm.filePath, data, 0644)
}

func (sm *SessionManager) Get(chatID string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.mapping[chatID]
}

func (sm *SessionManager) Set(chatID string, sessionID string) {
	sm.mu.Lock()
	sm.mapping[chatID] = sessionID
	sm.mu.Unlock()
	_ = sm.Save()
}

// MediaMessage extends ollama.Message with per-image kind metadata
type MediaMessage struct {
	ollama.Message
	ImageKinds []string `json:"image_kinds,omitempty"`
}

// Bot represents the Telegram polling bot
type Bot struct {
	cfg         config.Config
	client      *ollama.Client
	sessions    *sessions.Store
	sessManager *SessionManager
	memoryStore *memory.Store
	apiBase     string
	httpClient  *http.Client
}

func NewBot(cfg config.Config, client *ollama.Client) *Bot {
	token := cfg.TelegramBotToken
	return &Bot{
		cfg:         cfg,
		client:      client,
		sessions:    sessions.NewStore(cfg.SessionsPath),
		sessManager: NewSessionManager(cfg.SessionsPath),
		memoryStore: memory.NewStore(cfg.MemoryPath),
		apiBase:     "https://api.telegram.org/bot" + token,
		httpClient:  &http.Client{Timeout: 40 * time.Second},
	}
}

// Start initiates the long polling loop
func (b *Bot) Start(ctx context.Context) error {
	if err := b.sessManager.Load(); err != nil {
		return fmt.Errorf("failed to load telegram session mapping: %w", err)
	}

	// Verify ffmpeg presence and log clear warnings in console if missing
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		log.Println("ffmpeg detected. Multimedia audio features are fully enabled.")
	} else {
		log.Println("WARNING: 'ffmpeg' was not found in PATH.")
		log.Println("Voice notes and audio features require 'ffmpeg' to be installed.")
		log.Println("Without it, voice messages will fail with errors (e.g., status 500 image: unknown format from Ollama).")
		log.Println("Please install it manually using your platform's package manager:")
		switch runtime.GOOS {
		case "windows":
			log.Println("-> Windows (PowerShell): winget install Gyan.FFmpeg")
		case "darwin":
			log.Println("-> macOS: brew install ffmpeg")
		case "linux":
			log.Println("-> Linux: sudo apt install ffmpeg")
		default:
			log.Println("-> Please install 'ffmpeg' using your OS package manager.")
		}
	}

	log.Println("[Telegram] Polling loop started successfully")
	b.sendStartupNotification()
	offset := int64(0)

	for {
		select {
		case <-ctx.Done():
			log.Println("[Telegram] Polling loop stopped")
			return ctx.Err()
		default:
			updates, err := b.getUpdates(offset)
			if err != nil {
				log.Printf("[Telegram] Error fetching updates: %v, retrying in 5 seconds...", err)
				time.Sleep(5 * time.Second)
				continue
			}

			for _, update := range updates {
				if update.UpdateID >= offset {
					offset = update.UpdateID + 1
				}
				b.handleUpdate(update)
			}
		}
	}
}

func (b *Bot) getUpdates(offset int64) ([]Update, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30", b.apiBase, offset)
	resp, err := b.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	return apiResp.Result, nil
}

func (b *Bot) handleUpdate(update Update) {
	if update.Message != nil {
		b.handleMessage(update.Message)
	}
}

func (b *Bot) handleMessage(msg *Message) {
	chatID := msg.Chat.ID
	chatIDStr := fmt.Sprintf("%d", chatID)

	var fromID int64
	if msg.From != nil {
		fromID = msg.From.ID
	} else {
		fromID = chatID
	}

	if !b.isAuthorized(fromID) {
		log.Printf("[Telegram] Unauthorized access attempt from user ID: %d (chat ID: %d)", fromID, chatID)
		b.sendMessage(chatID, "⚠️ *Access Denied.*\nYou are not authorized to use this bot.", 0, "Markdown")
		return
	}

	// Handle standard command prefixes
	if msg.Text != "" && strings.HasPrefix(msg.Text, "/") {
		parts := strings.Fields(msg.Text)
		cmd := parts[0]
		args := ""
		if len(parts) > 1 {
			args = strings.Join(parts[1:], " ")
		}
		b.handleCommand(chatID, cmd, args)
		return
	}

	// Retrieve or initialize current session ID
	sessionID := b.sessManager.Get(chatIDStr)
	if sessionID == "" {
		sessionID = b.startNewSession(chatIDStr)
		b.sendMessage(chatID, "👋 Hello! I have initialized a new conversation session for you. Ask me anything, send me a photo, or send a voice message!", msg.MessageID, "Markdown")
	} else {
		// Confirm session exists on disk
		if _, err := b.sessions.Get(sessionID); err != nil {
			sessionID = b.startNewSession(chatIDStr)
		}
	}

	// Process message input asynchronously
	go b.processMessageInput(msg, sessionID)
}

func (b *Bot) isAuthorized(fromID int64) bool {
	if len(b.cfg.TelegramAuthorizedIDs) == 0 {
		return true
	}
	idStr := fmt.Sprintf("%d", fromID)
	for _, authID := range b.cfg.TelegramAuthorizedIDs {
		if strings.TrimSpace(authID) == idStr {
			return true
		}
	}
	return false
}

func (b *Bot) startNewSession(chatIDStr string) string {
	sessionID := sessions.GenerateID()
	sess := sessions.Session{
		ID:        sessionID,
		Title:     "Telegram Chat (" + chatIDStr + ")",
		Model:     b.cfg.OllamaDefaultModel,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = b.sessions.Save(sess)
	b.sessManager.Set(chatIDStr, sessionID)
	return sessionID
}

func (b *Bot) handleCommand(chatID int64, cmd string, args string) {
	chatIDStr := fmt.Sprintf("%d", chatID)
	switch cmd {
	case "/start":
		b.startNewSession(chatIDStr)
		b.sendMessage(chatID, "👋 *Welcome to OllamaBot on Telegram!*\n\nI am your local-first AI autonomous companion. You can chat with me, send images, or send voice messages.\n\n*Commands:*\n- `/new` - Start a new clean session\n- `/start` - Display this welcome message\n\nAsk me anything to get started!", 0, "Markdown")
	case "/new":
		b.startNewSession(chatIDStr)
		b.sendMessage(chatID, "🔄 *New session started!* Previous history cleared.", 0, "Markdown")
	default:
		b.sendMessage(chatID, "❌ Unknown command. Available commands: `/new` or `/start`", 0, "Markdown")
	}
}

func (b *Bot) processMessageInput(msg *Message, sessionID string) {
	chatID := msg.Chat.ID
	chatIDStr := fmt.Sprintf("%d", chatID)
	ctx := context.Background()

	_ = b.sendChatAction(chatID, "typing")

	var mediaBytes []byte
	var mediaKind string // "audio" or "image"
	var mediaName string

	if len(msg.Photo) > 0 {
		// Get largest photo size
		largestPhoto := msg.Photo[len(msg.Photo)-1]
		_ = b.sendChatAction(chatID, "upload_photo")
		fileInfo, err := b.getFile(largestPhoto.FileID)
		if err == nil {
			bytes, err := b.downloadFile(fileInfo.FilePath)
			if err == nil {
				mediaBytes = bytes
				mediaKind = "image"
				mediaName = filepath.Base(fileInfo.FilePath)
			}
		}
	} else if msg.Voice != nil {
		_ = b.sendChatAction(chatID, "record_voice")
		fileInfo, err := b.getFile(msg.Voice.FileID)
		if err == nil {
			bytes, err := b.downloadFile(fileInfo.FilePath)
			if err == nil {
				wavBytes, convErr := b.convertToWav(bytes)
				if convErr == nil {
					mediaBytes = wavBytes
					mediaName = strings.TrimSuffix(filepath.Base(fileInfo.FilePath), filepath.Ext(fileInfo.FilePath)) + ".wav"
				} else {
					log.Printf("Warning: Audio conversion failed: %v", convErr)
					if strings.Contains(convErr.Error(), "not found") {
						b.sendMessage(chatID, "⚠️ *Voice transcription is unavailable* because `ffmpeg` is not installed on this server.\n\n*How to install:*\n• *Windows (PowerShell):* `winget install Gyan.FFmpeg`\n• *macOS:* `brew install ffmpeg`\n• *Linux:* `sudo apt install ffmpeg`\n\nPlease contact the administrator to enable voice notes.", msg.MessageID, "Markdown")
						return
					}
					mediaBytes = bytes
					mediaName = filepath.Base(fileInfo.FilePath)
				}
				mediaKind = "audio"
			}
		}
	} else if msg.Audio != nil {
		_ = b.sendChatAction(chatID, "record_voice")
		fileInfo, err := b.getFile(msg.Audio.FileID)
		if err == nil {
			bytes, err := b.downloadFile(fileInfo.FilePath)
			if err == nil {
				wavBytes, convErr := b.convertToWav(bytes)
				if convErr == nil {
					mediaBytes = wavBytes
					mediaName = strings.TrimSuffix(filepath.Base(fileInfo.FilePath), filepath.Ext(fileInfo.FilePath)) + ".wav"
				} else {
					log.Printf("Warning: Audio conversion failed: %v", convErr)
					if strings.Contains(convErr.Error(), "not found") {
						b.sendMessage(chatID, "⚠️ *Voice transcription is unavailable* because `ffmpeg` is not installed on this server.\n\n*How to install:*\n• *Windows (PowerShell):* `winget install Gyan.FFmpeg`\n• *macOS:* `brew install ffmpeg`\n• *Linux:* `sudo apt install ffmpeg`\n\nPlease contact the administrator to enable voice notes.", msg.MessageID, "Markdown")
						return
					}
					mediaBytes = bytes
					mediaName = filepath.Base(fileInfo.FilePath)
				}
				mediaKind = "audio"
			}
		}
	}

	// 1. Load session and messages
	sess, err := b.sessions.Get(sessionID)
	if err != nil {
		sessionID = b.startNewSession(chatIDStr)
		sess, _ = b.sessions.Get(sessionID)
	}

	var history []rawMsg
	for _, rm := range sess.Messages {
		var m rawMsg
		if err := json.Unmarshal(rm, &m); err == nil {
			history = append(history, m)
		}
	}

	// 2. Append new user message with media if present
	userMsg := rawMsg{
		Role:    "user",
		Content: msg.Text,
	}

	if len(mediaBytes) > 0 {
		b64 := base64.StdEncoding.EncodeToString(mediaBytes)
		userMsg.Images = []string{b64}
		userMsg.ImageKinds = []string{mediaKind}
		userMsg.Attachments = []attachmentMeta{
			{
				Name: mediaName,
				Mime: getMimeType(mediaKind, mediaName),
				Kind: mediaKind,
				Data: b64,
			},
		}
	}

	history = append(history, userMsg)

	// 3. Convert session raw messages to runtime format
	var mediaMessages []MediaMessage
	for _, h := range history {
		var toolCalls []ollama.ToolCall
		for _, tcRaw := range h.ToolCalls {
			var tc ollama.ToolCall
			if err := json.Unmarshal(tcRaw, &tc); err == nil {
				toolCalls = append(toolCalls, tc)
			}
		}

		mediaMessages = append(mediaMessages, MediaMessage{
			Message: ollama.Message{
				Role:      h.Role,
				Content:   h.Content,
				Thinking:  h.Thinking,
				Images:    h.Images,
				Name:      h.Name,
				ToolCalls: toolCalls,
			},
			ImageKinds: h.ImageKinds,
		})
	}

	// 4. Initialize media router and preprocess attachments
	mr := router.New(b.client, router.Config{
		MainModel:   b.cfg.OllamaDefaultModel,
		VisionModel: b.cfg.OllamaModelVision,
		AudioModel:  b.cfg.OllamaModelAudio,
	})

	ollamaMessages, err := b.resolveTelegramMedia(ctx, mr, mediaMessages)
	if err != nil {
		log.Printf("[Telegram] Error resolving media: %v", err)
		b.sendMessage(chatID, "❌ Error pre-processing media: "+err.Error(), 0, "")
		return
	}

	// 5. Inject memory system instructions
	if b.cfg.OllamaModelEmbed != "" {
		ollamaMessages = append([]ollama.Message{{
			Role:    "system",
			Content: "You have access to long-term memory tools (memory_add, memory_search, memory_delete, memory_list). Manage your own memory proactively:\n- Store important facts, user preferences, decisions, and context using memory_add.\n- Search memory when the question may benefit from past knowledge using memory_search.\n- Delete outdated or incorrect information using memory_delete.\n- Review stored memories with memory_list before deciding what to add, update, or remove.\n- Consolidate: if you learn updated information, delete the old version and store the new one.\n- Prioritize: only store information that is likely to be useful later.",
		}}, ollamaMessages...)
	}

	// 6. Dynamically update personality from query
	if msg.Text != "" {
		_ = agent.UpdateSoulFromPrompt(msg.Text)
	}

	// 7. Inject SOUL.md system instruction
	if soulContent, err := agent.LoadSoul(); err == nil && soulContent != "" {
		ollamaMessages = append([]ollama.Message{{
			Role:    "system",
			Content: soulContent,
		}}, ollamaMessages...)
	}

	// 8. Instantiate agent registry and loop
	registry := tools.NewRegistry(b.cfg.WebSearchEnabled, b.cfg.Workspace, b.memoryStore, b.client, b.cfg.OllamaModelEmbed)
	a := agent.NewAgent(b.cfg, b.client, registry)
	handler := &telegramStreamHandler{bot: b, chatID: chatID}

	_ = b.sendChatAction(chatID, "typing")

	// 9. Execute agent multi-turn planning & tool calls loop
	finalHistory, err := a.Run(ctx, b.cfg.OllamaDefaultModel, ollamaMessages, true, handler)
	if err != nil {
		log.Printf("[Telegram] Agent loop execution failed: %v", err)
		b.sendMessage(chatID, "❌ Error during execution: "+err.Error(), 0, "")
		return
	}

	// 10. Persist full history details (thinking process and tool results) in session messages
	var newRawMessages []json.RawMessage
	for _, m := range finalHistory {
		if m.Role == "user" && len(m.Images) > 0 {
			rm := rawMsg{
				Role:        m.Role,
				Content:     m.Content,
				Thinking:    m.Thinking,
				Images:      m.Images,
				ImageKinds:  userMsg.ImageKinds,
				Attachments: userMsg.Attachments,
				Name:        m.Name,
			}
			rawBytes, _ := json.Marshal(rm)
			newRawMessages = append(newRawMessages, rawBytes)
		} else {
			var tcRaw []json.RawMessage
			for _, tc := range m.ToolCalls {
				tcBytes, _ := json.Marshal(tc)
				tcRaw = append(tcRaw, tcBytes)
			}
			rm := rawMsg{
				Role:      m.Role,
				Content:   m.Content,
				Thinking:  m.Thinking,
				Name:      m.Name,
				Images:    m.Images,
				ToolCalls: tcRaw,
			}
			rawBytes, _ := json.Marshal(rm)
			newRawMessages = append(newRawMessages, rawBytes)
		}
	}

	sess.Messages = newRawMessages
	_ = b.sessions.Save(sess)

	// 11. Send the final Synthesized text response back to the user
	var finalAnswer string
	for i := len(finalHistory) - 1; i >= 0; i-- {
		if finalHistory[i].Role == "assistant" && strings.TrimSpace(finalHistory[i].Content) != "" {
			finalAnswer = finalHistory[i].Content
			break
		}
	}

	if finalAnswer == "" {
		b.sendMessage(chatID, "⚠️ I did not generate a text response. Please try again.", msg.MessageID, "")
		return
	}

	chunks := splitMessage(finalAnswer, 4000)
	for _, chunk := range chunks {
		_, _ = b.sendMessage(chatID, chunk, msg.MessageID, "Markdown")
	}
}

func (b *Bot) resolveTelegramMedia(ctx context.Context, mr *router.Router, messages []MediaMessage) ([]ollama.Message, error) {
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	out := make([]ollama.Message, 0, len(messages))
	for i, msg := range messages {
		if msg.Role != "user" || i != lastUserIdx || len(msg.Images) == 0 {
			out = append(out, msg.Message)
			continue
		}

		var analyses []string
		var passthrough []string
		var audioTranscriptions []string

		type attachment struct {
			kind   string
			base64 string
		}

		var attachments []attachment
		for idx, b64 := range msg.Images {
			kind := "image"
			if idx < len(msg.ImageKinds) {
				kind = msg.ImageKinds[idx]
			}
			attachments = append(attachments, attachment{
				kind:   kind,
				base64: b64,
			})
		}

		// Pass 1: Process audio first
		for _, att := range attachments {
			if att.kind == "audio" {
				needsRouting := mr.NeedsMediaRouting(att.kind)
				if needsRouting {
					analysis, err := mr.AnalyzeAudio(ctx, att.base64, msg.Content)
					if err != nil {
						return nil, err
					}
					audioTranscriptions = append(audioTranscriptions, analysis)
					analyses = append(analyses, fmt.Sprintf("[Audio Transcription & Analysis]:\n%s", analysis))
				} else {
					passthrough = append(passthrough, att.base64)
				}
			}
		}

		// Construct image prompt by combining text prompt and audio transcriptions
		imagePrompt := msg.Content
		if len(audioTranscriptions) > 0 {
			combinedAudio := strings.Join(audioTranscriptions, "\n\n")
			if strings.TrimSpace(imagePrompt) != "" {
				imagePrompt = fmt.Sprintf("%s\n\n[Instruction/Context from Audio Transcription]:\n%s", imagePrompt, combinedAudio)
			} else {
				imagePrompt = fmt.Sprintf("Analyze this image based on the following instruction transcribed from audio:\n%s", combinedAudio)
			}
		}

		// Pass 2: Process images
		for _, att := range attachments {
			if att.kind != "audio" {
				needsRouting := mr.NeedsMediaRouting(att.kind)
				if needsRouting {
					analysis, err := mr.AnalyzeImage(ctx, att.base64, imagePrompt)
					if err != nil {
						return nil, err
					}
					logPrompt := imagePrompt
					if len(logPrompt) > 120 {
						logPrompt = logPrompt[:117] + "..."
					}
					analyses = append(analyses, fmt.Sprintf("[Image Analysis (Prompt: %s)]:\n%s", strings.ReplaceAll(logPrompt, "\n", " "), analysis))
				} else {
					passthrough = append(passthrough, att.base64)
				}
			}
		}

		if len(analyses) > 0 {
			assistantContent := "The user has attached media. The pre-processing analysis is as follows:\n\n" + strings.Join(analyses, "\n\n")
			out = append(out, ollama.Message{
				Role:    "assistant",
				Content: assistantContent,
			})
		}

		resolved := msg.Message
		resolved.Images = passthrough

		// Format and inject the audio transcription contextually into the final user prompt
		if len(audioTranscriptions) > 0 {
			combinedAudio := strings.Join(audioTranscriptions, "\n\n")
			hasPassthroughImages := len(passthrough) > 0
			hasUserContent := strings.TrimSpace(resolved.Content) != ""

			if hasPassthroughImages {
				if hasUserContent {
					resolved.Content = fmt.Sprintf("%s\n\n[The user also sent this audio transcription accompanying the image]:\n\"%s\"", resolved.Content, combinedAudio)
				} else {
					resolved.Content = fmt.Sprintf("[The user sent this audio transcription accompanying the image]:\n\"%s\"", combinedAudio)
				}
			} else {
				if hasUserContent {
					resolved.Content = fmt.Sprintf("%s\n\n[The user also sent this audio transcription]:\n\"%s\"", resolved.Content, combinedAudio)
				} else {
					resolved.Content = fmt.Sprintf("[The user sent only this audio transcription]:\n\"%s\"", combinedAudio)
				}
			}
		}

		if strings.TrimSpace(resolved.Content) == "" {
			if len(analyses) > 0 {
				resolved.Content = "Respond to the attached media analysis."
			} else if len(passthrough) > 0 {
				resolved.Content = "Analyze the attached media."
			}
		}

		out = append(out, resolved)
	}
	return out, nil
}

func (b *Bot) sendMessage(chatID int64, text string, replyToID int64, parseMode string) (int64, error) {
	type SendMessageRequest struct {
		ChatID           int64  `json:"chat_id"`
		Text             string `json:"text"`
		ParseMode        string `json:"parse_mode,omitempty"`
		ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
	}

	reqBody := SendMessageRequest{
		ChatID:           chatID,
		Text:             text,
		ParseMode:        parseMode,
		ReplyToMessageID: replyToID,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return 0, err
	}

	url := b.apiBase + "/sendMessage"
	resp, err := b.httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var apiResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      *struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return 0, err
	}

	if !apiResp.OK {
		if parseMode != "" && (strings.Contains(apiResp.Description, "parse") || strings.Contains(apiResp.Description, "markdown")) {
			log.Printf("[Telegram] Warning: markdown parsing failed (%s). Retrying as plain text.", apiResp.Description)
			return b.sendMessage(chatID, text, replyToID, "")
		}
		return 0, fmt.Errorf("telegram api error: %s", apiResp.Description)
	}

	if apiResp.Result != nil {
		return apiResp.Result.MessageID, nil
	}
	return 0, nil
}

func (b *Bot) sendChatAction(chatID int64, action string) error {
	type ChatActionRequest struct {
		ChatID int64  `json:"chat_id"`
		Action string `json:"action"`
	}

	reqBody := ChatActionRequest{
		ChatID: chatID,
		Action: action,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := b.apiBase + "/sendChatAction"
	resp, err := b.httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (b *Bot) getFile(fileID string) (*File, error) {
	url := b.apiBase + "/getFile?file_id=" + fileID
	resp, err := b.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp GetFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	if !apiResp.OK || apiResp.Result == nil {
		return nil, fmt.Errorf("failed to get file info from telegram")
	}

	return apiResp.Result, nil
}

func (b *Bot) downloadFile(filePath string) ([]byte, error) {
	token := b.cfg.TelegramBotToken
	url := "https://api.telegram.org/file/bot" + token + "/" + filePath
	resp, err := b.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download file, status: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// Custom StreamHandler for Telegram bot
type telegramStreamHandler struct {
	bot    *Bot
	chatID int64
}

func (h *telegramStreamHandler) OnThinking(delta string) {}

func (h *telegramStreamHandler) OnContent(delta string) {}

func (h *telegramStreamHandler) OnToolCall(call ollama.ToolCall) {}

func (h *telegramStreamHandler) OnToolStart(name string, args any) {
	_ = h.bot.sendChatAction(h.chatID, "typing")
	_, _ = h.bot.sendMessage(h.chatID, fmt.Sprintf("🔧 *Running tool:* `%s`...", name), 0, "Markdown")
}

func (h *telegramStreamHandler) OnToolResult(name string, result string) {}

func (h *telegramStreamHandler) OnMediaPreProcessing(content string) {
	_ = h.bot.sendChatAction(h.chatID, "typing")
}

// Struct re-definitions to remain completely self-contained
type rawMsg struct {
	Role           string            `json:"role"`
	Content        string            `json:"content,omitempty"`
	Thinking       string            `json:"thinking,omitempty"`
	Name           string            `json:"name,omitempty"`
	Images         []string          `json:"images,omitempty"`
	AttachmentRefs []string          `json:"attachment_refs,omitempty"`
	ImageKinds     []string          `json:"image_kinds,omitempty"`
	Attachments    []attachmentMeta  `json:"attachments,omitempty"`
	ToolCalls      []json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults    []json.RawMessage `json:"tool_results,omitempty"`
}

type attachmentMeta struct {
	Name string `json:"name,omitempty"`
	Mime string `json:"mime,omitempty"`
	Kind string `json:"kind,omitempty"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

func getMimeType(kind, name string) string {
	if kind == "audio" {
		return "audio/ogg"
	}
	if strings.HasSuffix(strings.ToLower(name), ".png") {
		return "image/png"
	}
	return "image/jpeg"
}

func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > maxLen {
		splitIdx := strings.LastIndex(text[:maxLen], "\n")
		if splitIdx == -1 {
			splitIdx = strings.LastIndex(text[:maxLen], " ")
		}
		if splitIdx == -1 || splitIdx < maxLen/2 {
			splitIdx = maxLen
		}

		chunks = append(chunks, strings.TrimSpace(text[:splitIdx]))
		text = strings.TrimSpace(text[splitIdx:])
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

func (b *Bot) sendStartupNotification() {
	if len(b.cfg.TelegramAuthorizedIDs) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("🚀 *OllamaBot Initialized*\n\n")

	sb.WriteString("🤖 *Active Models:*\n")
	sb.WriteString(fmt.Sprintf("• *Main:* `%s`\n", b.cfg.OllamaDefaultModel))
	if b.cfg.OllamaModelVision != "" {
		sb.WriteString(fmt.Sprintf("• *Vision:* `%s`\n", b.cfg.OllamaModelVision))
	}
	if b.cfg.OllamaModelAudio != "" {
		sb.WriteString(fmt.Sprintf("• *Audio:* `%s`\n", b.cfg.OllamaModelAudio))
	}
	if b.cfg.OllamaModelEmbed != "" {
		sb.WriteString(fmt.Sprintf("• *Memory:* `%s`\n", b.cfg.OllamaModelEmbed))
	}
	sb.WriteString("\n")

	sb.WriteString("🛠️ *Active Capabilities:*\n")
	sb.WriteString("• 💬 Local Chat\n")
	if b.cfg.OllamaModelVision != "" || b.cfg.OllamaDefaultModel != "" {
		sb.WriteString("• 👁️ Image Analysis\n")
	}
	if b.cfg.OllamaModelAudio != "" {
		sb.WriteString("• 🎙️ Voice Transcription\n")
	}
	if b.cfg.WebSearchEnabled {
		sb.WriteString("• 🔍 Web Search\n")
	}
	if b.cfg.OllamaModelEmbed != "" {
		sb.WriteString("• 💾 Long-Term Memory (RAG)\n")
	}

	messageText := strings.TrimSpace(sb.String())

	for _, authID := range b.cfg.TelegramAuthorizedIDs {
		id, err := parseChatID(authID)
		if err == nil {
			_, _ = b.sendMessage(id, messageText, 0, "Markdown")
		}
	}
}

func parseChatID(s string) (int64, error) {
	var val int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &val)
	return val, err
}

func (b *Bot) convertToWav(inputBytes []byte) ([]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	tempDir := filepath.Join(b.cfg.Workspace, "temp")
	_ = os.MkdirAll(tempDir, 0755)

	inputPath := filepath.Join(tempDir, fmt.Sprintf("temp_input_%d.bin", time.Now().UnixNano()))
	outputPath := filepath.Join(tempDir, fmt.Sprintf("temp_output_%d.wav", time.Now().UnixNano()))

	if err := os.WriteFile(inputPath, inputBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temporary input file: %w", err)
	}
	defer os.Remove(inputPath)

	log.Printf("[Telegram] Converting audio to WAV using ffmpeg...")
	cmd := exec.Command("ffmpeg", "-y", "-i", inputPath, "-acodec", "pcm_s16le", "-ac", "1", "-ar", "16000", outputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[Telegram] ffmpeg error: %v. Stderr: %s", err, stderr.String())
		return nil, fmt.Errorf("ffmpeg conversion failed: %w (stderr: %s)", err, stderr.String())
	}
	defer os.Remove(outputPath)

	wavBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read converted WAV file: %w", err)
	}

	log.Printf("[Telegram] Audio successfully converted to WAV, size: %d bytes", len(wavBytes))
	return wavBytes, nil
}
