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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"os/exec"
	"runtime"

	"github.com/jonathanhecl/ollamabot/internal/agent"
	"github.com/jonathanhecl/ollamabot/internal/cache"
	"github.com/jonathanhecl/ollamabot/internal/capabilities"
	"github.com/jonathanhecl/ollamabot/internal/config"
	"github.com/jonathanhecl/ollamabot/internal/learning"
	"github.com/jonathanhecl/ollamabot/internal/memory"
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/probe"
	"github.com/jonathanhecl/ollamabot/internal/router"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
	"github.com/jonathanhecl/ollamabot/internal/tools"
)

// Telegram API structures
type Update struct {
	UpdateID        int64                  `json:"update_id"`
	Message         *Message               `json:"message,omitempty"`
	CallbackQuery   *CallbackQuery         `json:"callback_query,omitempty"`
	MessageReaction *MessageReactionUpdate `json:"message_reaction,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type MessageReactionUpdate struct {
	MessageID   int64          `json:"message_id"`
	Chat        Chat           `json:"chat"`
	User        *User          `json:"user,omitempty"`
	Date        int64          `json:"date"`
	NewReaction []ReactionType `json:"new_reaction"`
	OldReaction []ReactionType `json:"old_reaction"`
}

type ReactionType struct {
	Type  string `json:"type"`  // "emoji" or "custom_emoji"
	Emoji string `json:"emoji"` // e.g. "👍", "👎"
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

type pendingClarification struct {
	ch      chan string
	options []string
}

// Bot represents the Telegram polling bot
type Bot struct {
	cfg              config.Config
	client           *ollama.Client
	sessions         *sessions.Store
	sessManager      *SessionManager
	memoryStore      *memory.Store
	autoMgr          *agent.AutonomousManager
	goalMgr          *agent.GoalManager
	apiBase          string
	httpClient       *http.Client
	approvalsMu      sync.Mutex
	approvals        map[string]chan bool
	clarificationsMu sync.Mutex
	clarifications   map[string]pendingClarification
	sleepMgr         *learning.SleepManager
	envPath          string
	msgIDMu          sync.RWMutex
	msgIDMap         map[string]map[int64]int // chatIDStr -> telegram_msg_id -> session_message_index
}

func NewBot(cfg config.Config, client *ollama.Client) *Bot {
	token := cfg.TelegramBotToken
	ms := memory.NewStore(cfg.MemoryPath)
	return &Bot{
		cfg:            cfg,
		client:         client,
		sessions:       sessions.NewStore(cfg.SessionsPath),
		sessManager:    NewSessionManager(cfg.SessionsPath),
		memoryStore:    ms,
		autoMgr:        agent.NewAutonomousManager(cfg, client, ms),
		goalMgr:        agent.NewGoalManager(cfg, client),
		apiBase:        "https://api.telegram.org/bot" + token,
		httpClient:     &http.Client{Timeout: 40 * time.Second},
		approvals:      make(map[string]chan bool),
		clarifications: make(map[string]pendingClarification),
		msgIDMap:       make(map[string]map[int64]int),
		envPath:        ".env",
	}
}

func NewBotWithEnv(cfg config.Config, client *ollama.Client, envPath string) *Bot {
	bot := NewBot(cfg, client)
	bot.envPath = envPath
	return bot
}

func (b *Bot) SetSleepManager(sm *learning.SleepManager) {
	b.sleepMgr = sm
}

func (b *Bot) SetGoalManager(gm *agent.GoalManager) {
	b.goalMgr = gm
}

// Start initiates the long polling loop
func (b *Bot) Start(ctx context.Context) error {
	if err := b.sessManager.Load(); err != nil {
		return fmt.Errorf("failed to load telegram session mapping: %w", err)
	}

	// Register notifiers for active/paused goals on startup
	if b.goalMgr != nil {
		b.sessManager.mu.RLock()
		for chatIDStr, sessionID := range b.sessManager.mapping {
			cID, err := strconv.ParseInt(chatIDStr, 10, 64)
			if err == nil {
				// Capture current chat ID in a local variable for the closure
				targetChatID := cID
				b.goalMgr.RegisterNotifier(sessionID, func(message string) {
					b.sendMessage(targetChatID, message, 0, "Markdown")
				})
			}
		}
		b.sessManager.mu.RUnlock()
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

	b.registerCommands()
	log.Println("[Telegram] Polling loop started successfully")
	b.sendStartupNotification()

	agent.OnTaskCompletion = func(proj agent.Project, task agent.ProjectTodo, err error) {
		b.notifyTaskCompletion(proj, task, err)
	}
	offset := int64(0)
	retryDelay := 5 * time.Second
	const maxRetryDelay = 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			log.Println("[Telegram] Polling loop stopped")
			return ctx.Err()
		default:
			updates, err := b.getUpdates(ctx, offset)
			if err != nil {
				log.Printf("[Telegram] Error fetching updates: %v, retrying in %v...", err, retryDelay)
				select {
				case <-ctx.Done():
					log.Println("[Telegram] Polling loop stopped during retry cooldown")
					return ctx.Err()
				case <-time.After(retryDelay):
				}

				// Exponential backoff
				retryDelay *= 2
				if retryDelay > maxRetryDelay {
					retryDelay = maxRetryDelay
				}
				continue
			}

			// Reset backoff on success
			retryDelay = 5 * time.Second

			for _, update := range updates {
				if update.UpdateID >= offset {
					offset = update.UpdateID + 1
				}
				b.handleUpdate(update)
			}
		}
	}
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]Update, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s", b.apiBase, offset, `["message","callback_query","message_reaction"]`)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpClient.Do(req)
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
	if b.sleepMgr != nil {
		b.sleepMgr.NotifyUserActivity()
	}
	if update.Message != nil {
		b.handleMessage(update.Message)
	}
	if update.CallbackQuery != nil {
		b.handleCallbackQuery(update.CallbackQuery)
	}
	if update.MessageReaction != nil {
		b.handleReaction(update.MessageReaction)
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
		// Confirm session exists on disk and has not expired (30-minute inactivity limit)
		sess, err := b.sessions.Get(sessionID)
		if err != nil {
			sessionID = b.startNewSession(chatIDStr)
		} else {
			expiryMin := b.cfg.TelegramSessionExpiryMin
			if expiryMin <= 0 {
				expiryMin = 30
			}
			if time.Since(sess.UpdatedAt) > time.Duration(expiryMin)*time.Minute {
				sessionID = b.startNewSession(chatIDStr)
				b.sendMessage(chatID, fmt.Sprintf("⏰ *Session expired due to %d minutes of inactivity.* Started a new session!", expiryMin), msg.MessageID, "Markdown")
			}
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
	sessions.NotifyUpdate(sessionID)
	return sessionID
}

func (b *Bot) autoGenerateSessionTitle(ctx context.Context, sessID string, assistantContent string) {
	log.Printf("[Telegram Auto-Name] Triggered for session ID: %s, Content length: %d", sessID, len(assistantContent))
	modelToUse := b.cfg.OllamaModelSubagent
	if strings.TrimSpace(modelToUse) == "" {
		modelToUse = b.cfg.OllamaDefaultModel
	}
	resp, err := b.client.Chat(ctx, ollama.ChatRequest{
		Model: modelToUse,
		Messages: []ollama.Message{
			{
				Role:    "system",
				Content: "Summarize the main topic of the response in an extremely short title (2 to 4 words). Do not use quotation marks, punctuation, or explanations. Respond with only the title.",
			},
			{
				Role:    "user",
				Content: assistantContent,
			},
		},
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		log.Printf("[Telegram Auto-Name] FAILED: %v", err)
		return
	}
	generatedTitle := strings.TrimSpace(resp.Message.Content)
	generatedTitle = strings.Trim(generatedTitle, `"'`)
	generatedTitle = strings.TrimRight(generatedTitle, ".!?")
	if generatedTitle != "" {
		log.Printf("[Telegram Auto-Name] Saving generated title: %s", generatedTitle)
		sess, err := b.sessions.Get(sessID)
		if err == nil {
			sess.Title = generatedTitle
			_ = b.sessions.Save(sess)
			sessions.NotifyUpdate(sessID)
		}
	}
}

func snapshotPath() string {
	if _, err := os.Stat("docs"); err == nil {
		return "docs/probe-cache.json"
	}
	return "probe-cache.json"
}

func (b *Bot) handleCommand(chatID int64, cmd string, args string) {
	chatIDStr := fmt.Sprintf("%d", chatID)
	switch cmd {
	case "/start":
		b.startNewSession(chatIDStr)
		b.sendMessage(chatID, "👋 *Welcome to OllamaBot on Telegram!*\n\nI am your local-first AI companion. You can chat with me, send images, or send voice messages.\n\n*Commands:*\n- `/new` - Start a new clean session\n- `/sessions` - List recent sessions (up to 10)\n- `/session <ID>` - Switch to a specific session\n- `/status` - Monitor VRAM and Ollama status\n- `/settings` - Change active models config\n- `/projects` - List autonomous workspace projects\n- `/memory <query>` - Query long-term semantic memory\n- `/reloadmodels` - Force reload models inventory & save snapshot\n- `/start` - Display this welcome message\n\nAsk me anything to get started!", 0, "Markdown")
	case "/new":
		b.startNewSession(chatIDStr)
		b.sendMessage(chatID, "🔄 *New session started!* Previous history cleared.", 0, "Markdown")
	case "/status":
		ctx := context.Background()
		version, err := b.client.Version(ctx)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("🔴 *Ollama Status:* Disconnected\nCould not connect to Ollama at %s:\n%v", b.cfg.OllamaBaseURL, err), 0, "Markdown")
			return
		}
		ps, err := b.client.Ps(ctx)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("🟢 *Ollama Status:* Connected (%s)\n⚠️ *Error querying loaded models:* %v", version.Version, err), 0, "Markdown")
			return
		}

		var totalVRAM int64
		var lines []string
		for _, m := range ps.Models {
			totalVRAM += m.SizeVRAM
			lines = append(lines, fmt.Sprintf("• `%s` (VRAM: %s, Expires in: %s)", m.Name, formatBytes(m.SizeVRAM), m.ExpiresAt))
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🟢 *Ollama Status:* Connected (%s)\n", version.Version))
		sb.WriteString(fmt.Sprintf("🧠 *VRAM Consumption:* %s\n\n", formatBytes(totalVRAM)))
		if len(lines) > 0 {
			sb.WriteString(fmt.Sprintf("*Loaded Models (%d):*\n%s", len(lines), strings.Join(lines, "\n")))
		} else {
			sb.WriteString("No models are currently loaded in VRAM.")
		}
		b.sendMessage(chatID, sb.String(), 0, "Markdown")
	case "/settings":
		text := b.buildSettingsText()
		markup := b.buildSettingsMarkup()
		_, _ = b.sendMessageWithMarkup(chatID, text, 0, "Markdown", markup)
	case "/projects":
		projects, err := b.autoMgr.ListProjects()
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Error listing projects:* %v", err), 0, "Markdown")
			return
		}
		if len(projects) == 0 {
			b.sendMessage(chatID, "📁 *Active Projects:*\nNo active projects found in workspace.", 0, "Markdown")
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📁 *Active Projects (%d):*\n\n", len(projects)))
		for i, proj := range projects {
			completed := 0
			for _, todo := range proj.Todos {
				if todo.Status == "completed" {
					completed++
				}
			}
			statusEmoji := "⏳"
			switch proj.Status {
			case "completed":
				statusEmoji = "✅"
			case "pending":
				statusEmoji = "💤"
			case "failed":
				statusEmoji = "❌"
			}
			sb.WriteString(fmt.Sprintf("%d. *Project:* `%s`\n", i+1, proj.Name))
			sb.WriteString(fmt.Sprintf("   • *Goal:* %s\n", proj.Goal))
			sb.WriteString(fmt.Sprintf("   • *Status:* %s `%s`\n", statusEmoji, proj.Status))
			sb.WriteString(fmt.Sprintf("   • *Tasks:* %d/%d completed\n", completed, len(proj.Todos)))
			if proj.CurrentTask != "" {
				sb.WriteString(fmt.Sprintf("   • *Current:* %s\n", proj.CurrentTask))
			}
			sb.WriteString("\n")
		}
		b.sendMessage(chatID, strings.TrimSpace(sb.String()), 0, "Markdown")
	case "/memory":
		if strings.TrimSpace(args) == "" {
			b.sendMessage(chatID, "ℹ️ *Usage:* `/memory <query>` to search semantic memory.", 0, "Markdown")
			return
		}
		if b.cfg.OllamaModelEmbed == "" {
			b.sendMessage(chatID, "⚠️ *Error:* No embedding model is configured.", 0, "Markdown")
			return
		}
		ctx := context.Background()
		resp, err := b.client.Embed(ctx, ollama.EmbedRequest{
			Model: b.cfg.OllamaModelEmbed,
			Input: args,
		})
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Error generating embedding:* %v", err), 0, "Markdown")
			return
		}
		if len(resp.Embeddings) == 0 {
			b.sendMessage(chatID, "❌ *Error:* Empty embedding response from Ollama.", 0, "Markdown")
			return
		}
		results := b.memoryStore.Search(resp.Embeddings[0], 3)
		if len(results) == 0 {
			b.sendMessage(chatID, fmt.Sprintf("💾 *Memory Search for:* \"%s\"\n\nNo matching memory records found.", args), 0, "Markdown")
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("💾 *Memory Search for:* \"%s\"\n\n", args))
		for i, r := range results {
			sb.WriteString(fmt.Sprintf("%d. *Score:* `%.2f`\n", i+1, r.Score))
			sb.WriteString(fmt.Sprintf("   • *Source:* `%s`\n", r.Entry.Source))
			sb.WriteString(fmt.Sprintf("   • *Text:* \"%s\"\n\n", r.Entry.Text))
		}
		b.sendMessage(chatID, strings.TrimSpace(sb.String()), 0, "Markdown")
	case "/reloadmodels":
		b.sendMessage(chatID, "⏳ *Reloading models...* Please wait.", 0, "Markdown")

		ctx := context.Background()
		runner := probe.NewRunner(b.client)
		version, err := b.client.Version(ctx)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Error reloading:* %v", err), 0, "Markdown")
			return
		}

		reports, err := runner.Inventory(ctx, b.cfg.OllamaProbeModels)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Error reloading:* %v", err), 0, "Markdown")
			return
		}

		ps, _ := b.client.Ps(ctx)

		cachePath := snapshotPath()
		var oldSnapshot cache.Snapshot
		if loaded, err := cache.Load(cachePath); err == nil {
			oldSnapshot = loaded
		}

		snapshot := cache.Snapshot{
			GeneratedAt:   time.Now(),
			BaseURL:       b.cfg.OllamaBaseURL,
			OllamaVersion: version.Version,
			Models:        reports,
			Running:       ps.Models,
			Expected:      oldSnapshot.Expected,
			ProbeRuns:     oldSnapshot.ProbeRuns,
		}

		if len(snapshot.Expected) == 0 {
			snapshot.Expected = []cache.ExpectedProbe{
				{Name: "models", Status: capabilities.Confirmed, Details: "Inventory from /api/tags and /api/show"},
				{Name: "audio", Status: capabilities.Pending, Details: "Audio remains pending unless an end-to-end REST payload is confirmed"},
				{Name: "video", Status: capabilities.Pending, Details: "Video remains pending; planned path is frame extraction plus vision"},
			}
		}

		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			_ = cache.Save(cachePath, snapshot)
		}

		var modelNames []string
		for _, m := range reports {
			status := "offline"
			for _, r := range ps.Models {
				if r.Name == m.Name || r.Model == m.Name {
					status = "loaded"
					break
				}
			}
			modelNames = append(modelNames, fmt.Sprintf("• `%s` (%s)", m.Name, status))
		}

		responseMsg := fmt.Sprintf("✅ *Models reloaded successfully!*\n\n*Detected Models (%d):*\n%s", len(reports), strings.Join(modelNames, "\n"))
		b.sendMessage(chatID, responseMsg, 0, "Markdown")
	case "/sessions":
		list, err := b.sessions.List()
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Error listing sessions:* %v", err), 0, "Markdown")
			return
		}
		if len(list) == 0 {
			b.sendMessage(chatID, "📂 *Sessions:* No sessions found.", 0, "Markdown")
			return
		}

		limit := 10
		if len(list) < limit {
			limit = len(list)
		}

		var sb strings.Builder
		sb.WriteString("📂 *Recent Sessions (Up to 10):*\n\n")
		for i := 0; i < limit; i++ {
			sess := list[i]
			title := sess.Title
			if title == "" {
				title = "Untitled"
			}
			timeStr := sess.UpdatedAt.Format("2006-01-02 15:04")
			sb.WriteString(fmt.Sprintf("%d. *%s*\n   • *ID:* `%s`\n   • *Updated:* %s\n\n", i+1, title, sess.ID, timeStr))
		}
		sb.WriteString("To switch to a specific session, type:\n`/session <session_id>`")
		b.sendMessage(chatID, sb.String(), 0, "Markdown")
	case "/session":
		sessionID := strings.TrimSpace(args)
		if sessionID == "" {
			b.sendMessage(chatID, "ℹ️ *Usage:* `/session <session_id>` to switch to a specific session.", 0, "Markdown")
			return
		}
		sess, err := b.sessions.Get(sessionID)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Session not found:* `%s` does not exist.", sessionID), 0, "Markdown")
			return
		}
		title := sess.Title
		if title == "" {
			title = "Untitled"
		}
		b.sessManager.Set(chatIDStr, sessionID)
		b.sendMessage(chatID, fmt.Sprintf("🔄 *Switched to session:* \"%s\"\n• *ID:* `%s`", title, sessionID), 0, "Markdown")
	case "/goal":
		if b.goalMgr == nil {
			b.sendMessage(chatID, "❌ *Error:* Goal system is not initialized.", 0, "Markdown")
			return
		}

		sessionID := b.sessManager.Get(chatIDStr)
		if sessionID == "" {
			sessionID = b.startNewSession(chatIDStr)
		}

		// Register notifier dynamically
		b.goalMgr.RegisterNotifier(sessionID, func(message string) {
			b.sendMessage(chatID, message, 0, "Markdown")
		})

		trimmedArgs := strings.TrimSpace(args)
		if trimmedArgs == "" {
			sess, err := b.sessions.Get(sessionID)
			if err != nil || sess.GoalObjective == "" {
				b.sendMessage(chatID, "ℹ️ *Usage:* `/goal <objective>` to start a persistent task.\nOther commands:\n• `/goal pause` - Pause active goal\n• `/goal resume` - Resume paused goal\n• `/goal clear` - Clear current goal", 0, "Markdown")
				return
			}
			b.sendMessage(chatID, fmt.Sprintf("🎯 *Active Goal:* %s\n\n*Status:* `%s`\n*Last check:* %s", sess.GoalObjective, sess.GoalStatus, sess.GoalReasoning), 0, "Markdown")
			return
		}

		var err error
		switch trimmedArgs {
		case "pause":
			err = b.goalMgr.PauseGoal(sessionID)
		case "resume":
			err = b.goalMgr.ResumeGoal(sessionID)
		case "clear":
			err = b.goalMgr.ClearGoal(sessionID)
		default:
			err = b.goalMgr.StartGoal(sessionID, trimmedArgs)
		}

		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("❌ *Error:* %v", err), 0, "Markdown")
		} else {
			sess, err := b.sessions.Get(sessionID)
			if err == nil {
				if trimmedArgs == "clear" {
					b.sendMessage(chatID, "🧹 *Goal cleared successfully.*", 0, "Markdown")
				} else if trimmedArgs == "pause" {
					b.sendMessage(chatID, "⏸️ *Goal paused.*", 0, "Markdown")
				} else if trimmedArgs == "resume" {
					b.sendMessage(chatID, "▶️ *Goal resumed.*", 0, "Markdown")
				} else {
					b.sendMessage(chatID, fmt.Sprintf("🚀 *Goal started in background!*\nObjective: *%s*", sess.GoalObjective), 0, "Markdown")
				}
				sessions.NotifyUpdate(sessionID)
			}
		}
	default:
		b.sendMessage(chatID, "❌ Unknown command. Available commands: `/new`, `/sessions`, `/session`, `/status`, `/settings`, `/projects`, `/memory`, `/reloadmodels`, `/goal`, `/start`", 0, "Markdown")
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
		Role:      "user",
		Content:   msg.Text,
		Timestamp: time.Now().Format(time.RFC3339),
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

	// Save the user message immediately so that the Web UI gets notified in real-time
	var initialRawMessages []json.RawMessage
	for _, hm := range history {
		rawBytes, _ := json.Marshal(hm)
		initialRawMessages = append(initialRawMessages, rawBytes)
	}
	sess.Messages = initialRawMessages
	_ = b.sessions.Save(sess)
	sessions.NotifyUpdate(sessionID)

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
	// 8. Instantiate agent registry and loop
	registry := tools.NewRegistry(b.cfg.WebSearchEnabled, b.cfg.Workspace, b.memoryStore, b.client, b.cfg.OllamaModelEmbed, tools.SearchConfig{
		Providers:    b.cfg.SearchProviders,
		BraveAPIKey:  b.cfg.BraveSearchAPIKey,
		TavilyAPIKey: b.cfg.TavilyAPIKey,
	})
	registry.SetApprovalHandler(&telegramApprovalHandler{
		bot:    b,
		chatID: chatID,
	})
	registry.SetClarificationHandler(&telegramClarificationHandler{
		bot:    b,
		chatID: chatID,
	})
	a := agent.NewAgent(b.cfg, b.client, registry)
	handler := &telegramStreamHandler{
		bot:         b,
		chatID:      chatID,
		sessionID:   sessionID,
		baseHistory: history,
	}

	_ = b.sendChatAction(chatID, "typing")

	// 9. Execute agent multi-turn planning & tool calls loop
	finalHistory, err := a.Run(ctx, b.cfg.OllamaDefaultModel, ollamaMessages, true, handler)
	if err != nil {
		log.Printf("[Telegram] Agent loop execution failed: %v", err)
		b.sendMessage(chatID, "❌ Error during execution: "+err.Error(), 0, "")
		return
	}

	// 10. Persist full history details (thinking process and tool results) in session messages
	var userAssistantTimestamps []string
	var historyUserMsgs []rawMsg
	for _, hm := range history {
		if hm.Role == "user" || hm.Role == "assistant" {
			userAssistantTimestamps = append(userAssistantTimestamps, hm.Timestamp)
		}
		if hm.Role == "user" {
			historyUserMsgs = append(historyUserMsgs, hm)
		}
	}

	userMsgIdx := 0
	uaIdx := 0
	var newRawMessages []json.RawMessage
	for _, m := range finalHistory {
		msgTimestamp := ""
		if m.Role == "user" || m.Role == "assistant" {
			if uaIdx < len(userAssistantTimestamps) {
				msgTimestamp = userAssistantTimestamps[uaIdx]
				uaIdx++
			}
			if msgTimestamp == "" {
				msgTimestamp = time.Now().Format(time.RFC3339)
			}
		}

		if m.Role == "user" {
			var origUserMsg rawMsg
			if userMsgIdx < len(historyUserMsgs) {
				origUserMsg = historyUserMsgs[userMsgIdx]
				userMsgIdx++
			}

			rm := rawMsg{
				Role:        m.Role,
				Content:     m.Content,
				Thinking:    m.Thinking,
				Images:      origUserMsg.Images,
				ImageKinds:  origUserMsg.ImageKinds,
				Attachments: origUserMsg.Attachments,
				Name:        m.Name,
				Timestamp:   msgTimestamp,
			}
			if len(rm.Images) == 0 && len(m.Images) > 0 {
				rm.Images = m.Images
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
				Timestamp: msgTimestamp,
			}
			rawBytes, _ := json.Marshal(rm)
			newRawMessages = append(newRawMessages, rawBytes)
		}
	}

	sess.Messages = newRawMessages
	_ = b.sessions.Save(sess)
	sessions.NotifyUpdate(sessionID)

	// Count user and assistant messages to trigger auto-naming on first exchange
	var userMsgsCount, assistantMsgsCount int
	for _, rm := range sess.Messages {
		var m rawMsg
		if err := json.Unmarshal(rm, &m); err == nil {
			if m.Role == "user" {
				userMsgsCount++
			} else if m.Role == "assistant" {
				assistantMsgsCount++
			}
		}
	}

	// 11. Send the final Synthesized text response back to the user
	var finalAnswer string
	for i := len(finalHistory) - 1; i >= 0; i-- {
		if finalHistory[i].Role == "assistant" && strings.TrimSpace(finalHistory[i].Content) != "" {
			finalAnswer = finalHistory[i].Content
			break
		}
	}

	// Strip any residual thinking tokens (<think>, <thought>, ...) before sending.
	finalAnswer = agent.CleanThinkingTokens(finalAnswer)

	if finalAnswer == "" {
		b.sendMessage(chatID, "⚠️ I did not generate a text response. Please try again.", 0, "")
		return
	}

	chunks := splitMessage(finalAnswer, 4000)
	for _, chunk := range chunks {
		sentMsgID, _ := b.sendMessage(chatID, toTelegramHTML(chunk), 0, "HTML")
		if sentMsgID > 0 {
			// Find the index of the last assistant message in the session
			lastAssistantIdx := -1
			for i := len(newRawMessages) - 1; i >= 0; i-- {
				var m rawMsg
				if err := json.Unmarshal(newRawMessages[i], &m); err == nil && m.Role == "assistant" {
					lastAssistantIdx = i
					break
				}
			}
			if lastAssistantIdx >= 0 {
				b.msgIDMu.Lock()
				if b.msgIDMap[chatIDStr] == nil {
					b.msgIDMap[chatIDStr] = make(map[int64]int)
				}
				b.msgIDMap[chatIDStr][sentMsgID] = lastAssistantIdx
				b.msgIDMu.Unlock()
			}
		}
	}

	if b.cfg.SessionAutoName && sessions.IsDefaultTitle(sess.Title) && finalAnswer != "" {
		b.autoGenerateSessionTitle(ctx, sessionID, finalAnswer)
	}
}

func (b *Bot) handleReaction(reaction *MessageReactionUpdate) {
	chatID := reaction.Chat.ID
	chatIDStr := fmt.Sprintf("%d", chatID)

	// Determine reaction type from new_reaction list
	var reactionStr string
	for _, r := range reaction.NewReaction {
		if r.Type == "emoji" {
			switch r.Emoji {
			case "\U0001F44D": // 👍
				reactionStr = "positive"
			case "\U0001F44E": // 👎
				reactionStr = "negative"
			}
			if reactionStr != "" {
				break
			}
		}
	}

	if reactionStr == "" {
		return // Not a thumbs up/down reaction, ignore
	}

	// Look up which session this chat belongs to
	sessionID := b.sessManager.Get(chatIDStr)
	if sessionID == "" {
		log.Printf("[Telegram] Reaction on chat %s but no active session found", chatIDStr)
		return
	}

	// Look up the message index from our mapping
	b.msgIDMu.RLock()
	chatMap, ok := b.msgIDMap[chatIDStr]
	b.msgIDMu.RUnlock()
	if !ok {
		log.Printf("[Telegram] Reaction on chat %s but no message ID map found", chatIDStr)
		return
	}

	msgIdx, ok := chatMap[reaction.MessageID]
	if !ok {
		log.Printf("[Telegram] Reaction on message %d in chat %s but message not tracked", reaction.MessageID, chatIDStr)
		return
	}

	// Save feedback to the session
	fb := sessions.Feedback{
		MessageIndex: msgIdx,
		Reaction:     reactionStr,
		Timestamp:    time.Now(),
	}

	if err := b.sessions.SaveFeedback(sessionID, fb); err != nil {
		log.Printf("[Telegram] Failed to save feedback: %v", err)
		return
	}
	sessions.NotifyUpdate(sessionID)

	log.Printf("[Telegram] Saved %s feedback for message #%d in session %s", reactionStr, msgIdx, sessionID)
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

		if strings.TrimSpace(resolved.Content) == "" && len(analyses) > 0 {
			resolved.Content = "Respond to the attached media analysis."
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
		log.Printf("[Telegram] sendMessage error: parseMode=%q desc=%q textPreview=%q", parseMode, apiResp.Description, truncate(text, 80))
		if parseMode != "" && (strings.Contains(apiResp.Description, "parse") || strings.Contains(apiResp.Description, "markdown") || strings.Contains(apiResp.Description, "entity")) {
			log.Printf("[Telegram] Warning: %s parsing failed (%s). Retrying as plain text.", parseMode, apiResp.Description)
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
	bot            *Bot
	chatID         int64
	sessionID      string
	baseHistory    []rawMsg
	activeMessages []rawMsg
	lastNotifyTime time.Time
	mu             sync.Mutex
}

func (h *telegramStreamHandler) getOrCreateAssistantMsg() *rawMsg {
	if len(h.activeMessages) == 0 || h.activeMessages[len(h.activeMessages)-1].Role != "assistant" {
		h.activeMessages = append(h.activeMessages, rawMsg{
			Role:      "assistant",
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
	return &h.activeMessages[len(h.activeMessages)-1]
}

func (h *telegramStreamHandler) notifyUpdate(force bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	if !force && now.Sub(h.lastNotifyTime) < 300*time.Millisecond {
		return
	}
	h.lastNotifyTime = now

	sess, err := h.bot.sessions.Get(h.sessionID)
	if err != nil {
		return
	}

	var allMessages []json.RawMessage
	for _, m := range h.baseHistory {
		rawBytes, _ := json.Marshal(m)
		allMessages = append(allMessages, rawBytes)
	}
	for _, m := range h.activeMessages {
		rawBytes, _ := json.Marshal(m)
		allMessages = append(allMessages, rawBytes)
	}

	sess.Messages = allMessages
	_ = h.bot.sessions.Save(sess)
	sessions.NotifyUpdate(h.sessionID)
}

func (h *telegramStreamHandler) OnThinking(delta string) {
	_ = h.bot.sendChatAction(h.chatID, "typing")
	h.mu.Lock()
	msg := h.getOrCreateAssistantMsg()
	msg.Thinking += delta
	h.mu.Unlock()
	h.notifyUpdate(false)
}

func (h *telegramStreamHandler) OnContent(delta string) {
	_ = h.bot.sendChatAction(h.chatID, "typing")
	h.mu.Lock()
	msg := h.getOrCreateAssistantMsg()
	msg.Content += delta
	h.mu.Unlock()
	h.notifyUpdate(false)
}

func (h *telegramStreamHandler) OnToolCall(call ollama.ToolCall) {
	h.mu.Lock()
	msg := h.getOrCreateAssistantMsg()
	tcBytes, _ := json.Marshal(call)

	found := false
	for _, existing := range msg.ToolCalls {
		var existingCall ollama.ToolCall
		if err := json.Unmarshal(existing, &existingCall); err == nil {
			if existingCall.Function.Name == call.Function.Name {
				found = true
				break
			}
		}
	}
	if !found {
		msg.ToolCalls = append(msg.ToolCalls, tcBytes)
	}
	h.mu.Unlock()
	h.notifyUpdate(false)
}

func (h *telegramStreamHandler) OnToolStart(name string, args any) {
	_ = h.bot.sendChatAction(h.chatID, "typing")
	_, _ = h.bot.sendMessage(h.chatID, fmt.Sprintf("🔧 *Running tool:* `%s`...", name), 0, "Markdown")
}

func (h *telegramStreamHandler) OnToolResult(name string, result string) {
	h.mu.Lock()
	h.activeMessages = append(h.activeMessages, rawMsg{
		Role:      "tool",
		Name:      name,
		Content:   result,
		Timestamp: time.Now().Format(time.RFC3339),
	})
	h.mu.Unlock()
	h.notifyUpdate(true)
}

func (h *telegramStreamHandler) OnMediaPreProcessing(content string) {
	_ = h.bot.sendChatAction(h.chatID, "typing")
	h.mu.Lock()
	h.activeMessages = append(h.activeMessages, rawMsg{
		Role:      "assistant",
		Content:   content,
		Timestamp: time.Now().Format(time.RFC3339),
	})
	h.mu.Unlock()
	h.notifyUpdate(true)
}
func (h *telegramStreamHandler) OnDone(resp ollama.ChatResponse) {}

// Struct re-definitions to remain completely self-contained
type rawMsg struct {
	Role           string            `json:"role"`
	Content        string            `json:"content,omitempty"`
	Thinking       string            `json:"thinking,omitempty"`
	Name           string            `json:"name,omitempty"`
	Timestamp      string            `json:"timestamp,omitempty"`
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
		if strings.HasSuffix(strings.ToLower(name), ".wav") {
			return "audio/wav"
		}
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// toTelegramHTML converts standard Markdown constructs to Telegram HTML format.
func toTelegramHTML(text string) string {
	log.Printf("[Telegram] toTelegramHTML input preview: %s", truncate(text, 120))
	// 1. Protect code blocks
	var codeBlocks []string
	reCodeBlock := regexp.MustCompile("(?s)```(?:\\w*)?\\n?(.*?)```")
	text = reCodeBlock.ReplaceAllStringFunc(text, func(match string) string {
		m := reCodeBlock.FindStringSubmatch(match)
		if len(m) > 1 {
			codeBlocks = append(codeBlocks, m[1])
		} else {
			codeBlocks = append(codeBlocks, "")
		}
		return fmt.Sprintf("\x00CODEBLOCK%d\x00", len(codeBlocks)-1)
	})

	// 2. Protect inline code
	var inlineCodes []string
	reInlineCode := regexp.MustCompile("`([^`]+)`")
	text = reInlineCode.ReplaceAllStringFunc(text, func(match string) string {
		m := reInlineCode.FindStringSubmatch(match)
		if len(m) > 1 {
			inlineCodes = append(inlineCodes, m[1])
		} else {
			inlineCodes = append(inlineCodes, "")
		}
		return fmt.Sprintf("\x00INLINECODE%d\x00", len(inlineCodes)-1)
	})

	// 3. Escape HTML special chars
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")

	// 4. Convert headings
	reHeading := regexp.MustCompile(`(?m)^#{1,6}\s*(.+)$`)
	text = reHeading.ReplaceAllString(text, `<b>$1</b>`)

	// 5. Convert bold
	reBold := regexp.MustCompile(`\*\*(.+?)\*\*`)
	text = reBold.ReplaceAllString(text, `<b>$1</b>`)

	// 6. Convert italic (_italic_)
	reItalic := regexp.MustCompile(`_(.+?)_`)
	text = reItalic.ReplaceAllString(text, `<i>$1</i>`)

	// 7. Convert links
	reLink := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	text = reLink.ReplaceAllString(text, `<a href="$2">$1</a>`)

	// 8. Convert bullet lists
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		leading := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "- ") {
			lines[i] = leading + "• " + trimmed[2:]
		}
	}
	text = strings.Join(lines, "\n")

	// 9. Restore code blocks
	for i, block := range codeBlocks {
		escaped := strings.ReplaceAll(block, "&", "&amp;")
		escaped = strings.ReplaceAll(escaped, "<", "&lt;")
		escaped = strings.ReplaceAll(escaped, ">", "&gt;")
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00CODEBLOCK%d\x00", i), fmt.Sprintf("<pre><code>%s</code></pre>", escaped))
	}

	// 10. Restore inline code
	for i, code := range inlineCodes {
		escaped := strings.ReplaceAll(code, "&", "&amp;")
		escaped = strings.ReplaceAll(escaped, "<", "&lt;")
		escaped = strings.ReplaceAll(escaped, ">", "&gt;")
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00INLINECODE%d\x00", i), fmt.Sprintf("<code>%s</code>", escaped))
	}

	log.Printf("[Telegram] toTelegramHTML output preview: %s", truncate(text, 120))
	return text
}

func (b *Bot) sendStartupNotification() {
	if !b.cfg.TelegramStartupNotification {
		return
	}
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

type telegramClarificationHandler struct {
	bot    *Bot
	chatID int64
}

func (h *telegramClarificationHandler) RequestClarification(ctx context.Context, question string, options []string) (string, error) {
	clarifyID := fmt.Sprintf("tgc_%d", time.Now().UnixNano())
	ch := make(chan string, 1)

	h.bot.clarificationsMu.Lock()
	h.bot.clarifications[clarifyID] = pendingClarification{
		ch:      ch,
		options: options,
	}
	h.bot.clarificationsMu.Unlock()

	defer func() {
		h.bot.clarificationsMu.Lock()
		delete(h.bot.clarifications, clarifyID)
		h.bot.clarificationsMu.Unlock()
	}()

	var rows [][]InlineKeyboardButton
	for idx, opt := range options {
		rows = append(rows, []InlineKeyboardButton{
			{
				Text:         opt,
				CallbackData: fmt.Sprintf("clarify:%s:%d", clarifyID, idx),
			},
		})
	}

	markup := &InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}

	text := fmt.Sprintf("❓ *Clarification Required*\n\n%s", question)
	msgID, err := h.bot.sendMessageWithMarkup(h.chatID, text, 0, "Markdown", markup)
	if err != nil {
		return "", fmt.Errorf("failed to send Telegram clarification request: %w", err)
	}

	select {
	case chosenOption := <-ch:
		statusText := fmt.Sprintf("💬 *Selected option:* %s", chosenOption)
		_ = h.bot.editMessageText(h.chatID, msgID, text+"\n\n"+statusText, "", nil)
		return chosenOption, nil
	case <-ctx.Done():
		chosen := selectDefaultOption(options)
		_ = h.bot.editMessageText(h.chatID, msgID, text+fmt.Sprintf("\n\n⚠️ *Cancelled:* proceeding with default option: %s", chosen), "", nil)
		return fmt.Sprintf("Clarification was cancelled or timed out. Proceeding with default option: %s", chosen), nil
	case <-time.After(5 * time.Minute):
		chosen := selectDefaultOption(options)
		statusText := fmt.Sprintf("⚠️ *Timed out:* auto-selected option: %s", chosen)
		_ = h.bot.editMessageText(h.chatID, msgID, text+"\n\n"+statusText, "", nil)
		return chosen, nil
	}
}

type telegramApprovalHandler struct {
	bot    *Bot
	chatID int64
}

func (h *telegramApprovalHandler) RequestApproval(ctx context.Context, toolName string, args map[string]any) (bool, error) {
	approvalID := fmt.Sprintf("tg_%d_%s", time.Now().UnixNano(), toolName)
	ch := make(chan bool, 1)

	h.bot.approvalsMu.Lock()
	h.bot.approvals[approvalID] = ch
	h.bot.approvalsMu.Unlock()

	defer func() {
		h.bot.approvalsMu.Lock()
		delete(h.bot.approvals, approvalID)
		h.bot.approvalsMu.Unlock()
	}()

	argsJSON, _ := json.MarshalIndent(args, "", "  ")
	text := fmt.Sprintf("🛡️ *Security Confirmation Required*\n\nThe AI agent is attempting to execute a potentially risky action:\n\n*Tool:* `%s`\n*Arguments:*\n```json\n%s\n```\n\nDo you approve this execution?", toolName, string(argsJSON))

	markup := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Approve ✅", CallbackData: "approve:" + approvalID},
				{Text: "Deny ❌", CallbackData: "deny:" + approvalID},
			},
		},
	}

	msgID, err := h.bot.sendMessageWithMarkup(h.chatID, text, 0, "Markdown", markup)
	if err != nil {
		return false, fmt.Errorf("failed to send Telegram approval request: %w", err)
	}

	select {
	case approved := <-ch:
		var statusText string
		if approved {
			statusText = fmt.Sprintf("✅ *Approved:* executed `%s`", toolName)
		} else {
			statusText = fmt.Sprintf("❌ *Denied:* skipped `%s`", toolName)
		}
		_ = h.bot.editMessageText(h.chatID, msgID, text+"\n\n"+statusText, "", nil)
		return approved, nil
	case <-ctx.Done():
		_ = h.bot.editMessageText(h.chatID, msgID, text+"\n\n⚠️ *Cancelled:* request timed out or was aborted.", "", nil)
		return false, ctx.Err()
	case <-time.After(5 * time.Minute):
		_ = h.bot.editMessageText(h.chatID, msgID, text+"\n\n⚠️ *Timed out:* auto-denied after 5 minutes.", "", nil)
		return false, fmt.Errorf("approval timeout")
	}
}

func (b *Bot) handleCallbackQuery(cb *CallbackQuery) {
	if !b.isAuthorized(cb.From.ID) {
		log.Printf("[Telegram] Unauthorized callback query attempt from user ID: %d", cb.From.ID)
		_ = b.answerCallbackQuery(cb.ID, "⚠️ Unauthorized", true)
		return
	}

	data := cb.Data
	if strings.HasPrefix(data, "settings_role:") {
		b.handleSettingsRoleCallback(cb)
		return
	}
	if strings.HasPrefix(data, "settings_model:") {
		b.handleSettingsModelCallback(cb)
		return
	}

	if strings.HasPrefix(data, "clarify:") {
		parts := strings.Split(data, ":") // clarify:id:index
		if len(parts) < 3 {
			_ = b.answerCallbackQuery(cb.ID, "⚠️ Invalid action", false)
			return
		}
		clarifyID := parts[1]
		idxStr := parts[2]
		var idx int
		if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil {
			_ = b.answerCallbackQuery(cb.ID, "⚠️ Invalid option index", false)
			return
		}

		b.clarificationsMu.Lock()
		pc, ok := b.clarifications[clarifyID]
		b.clarificationsMu.Unlock()

		if !ok {
			_ = b.answerCallbackQuery(cb.ID, "⚠️ Clarification expired or not found", true)
			if cb.Message != nil {
				_ = b.editMessageText(cb.Message.Chat.ID, cb.Message.MessageID, cb.Message.Text+"\n\n⚠️ *Expired:* request is no longer active.", "", nil)
			}
			return
		}

		if idx < 0 || idx >= len(pc.options) {
			_ = b.answerCallbackQuery(cb.ID, "⚠️ Option out of range", true)
			return
		}

		chosen := pc.options[idx]
		select {
		case pc.ch <- chosen:
			_ = b.answerCallbackQuery(cb.ID, "Clarification submitted", false)
		default:
			_ = b.answerCallbackQuery(cb.ID, "⚠️ Already answered", true)
		}
		return
	}

	if !strings.HasPrefix(data, "approve:") && !strings.HasPrefix(data, "deny:") {
		_ = b.answerCallbackQuery(cb.ID, "Unknown action", false)
		return
	}

	parts := strings.SplitN(data, ":", 2)
	action := parts[0]
	approvalID := parts[1]

	b.approvalsMu.Lock()
	ch, ok := b.approvals[approvalID]
	b.approvalsMu.Unlock()

	if !ok {
		_ = b.answerCallbackQuery(cb.ID, "⚠️ Request expired or not found", true)
		if cb.Message != nil {
			_ = b.editMessageText(cb.Message.Chat.ID, cb.Message.MessageID, cb.Message.Text+"\n\n⚠️ *Expired:* request is no longer active.", "", nil)
		}
		return
	}

	approved := action == "approve"
	select {
	case ch <- approved:
		_ = b.answerCallbackQuery(cb.ID, "Response processed", false)
	default:
		_ = b.answerCallbackQuery(cb.ID, "⚠️ Already answered", true)
	}
}

func (b *Bot) answerCallbackQuery(callbackQueryID string, text string, showAlert bool) error {
	type AnswerCallbackQueryRequest struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
		ShowAlert       bool   `json:"show_alert,omitempty"`
	}

	reqBody := AnswerCallbackQueryRequest{
		CallbackQueryID: callbackQueryID,
		Text:            text,
		ShowAlert:       showAlert,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := b.apiBase + "/answerCallbackQuery"
	resp, err := b.httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (b *Bot) editMessageText(chatID int64, messageID int64, text string, parseMode string, markup *InlineKeyboardMarkup) error {
	type EditMessageTextRequest struct {
		ChatID      int64                 `json:"chat_id"`
		MessageID   int64                 `json:"message_id"`
		Text        string                `json:"text"`
		ParseMode   string                `json:"parse_mode,omitempty"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}

	reqBody := EditMessageTextRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   parseMode,
		ReplyMarkup: markup,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := b.apiBase + "/editMessageText"
	resp, err := b.httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (b *Bot) sendMessageWithMarkup(chatID int64, text string, replyToID int64, parseMode string, markup *InlineKeyboardMarkup) (int64, error) {
	type SendMessageRequest struct {
		ChatID           int64                 `json:"chat_id"`
		Text             string                `json:"text"`
		ParseMode        string                `json:"parse_mode,omitempty"`
		ReplyToMessageID int64                 `json:"reply_to_message_id,omitempty"`
		ReplyMarkup      *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}

	reqBody := SendMessageRequest{
		ChatID:           chatID,
		Text:             text,
		ParseMode:        parseMode,
		ReplyToMessageID: replyToID,
		ReplyMarkup:      markup,
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
			return b.sendMessageWithMarkup(chatID, text, replyToID, "", markup)
		}
		return 0, fmt.Errorf("telegram api error: %s", apiResp.Description)
	}

	if apiResp.Result != nil {
		return apiResp.Result.MessageID, nil
	}
	return 0, nil
}

func selectDefaultOption(options []string) string {
	if len(options) == 0 {
		return ""
	}
	for _, opt := range options {
		low := strings.ToLower(opt)
		if strings.Contains(low, "recommended") || strings.Contains(low, "recomendado") || strings.Contains(low, "default") || strings.Contains(low, "predeterminado") {
			return opt
		}
	}
	return options[0]
}

func (b *Bot) buildSettingsMarkup() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Configure Main 🤖", CallbackData: "settings_role:main"},
				{Text: "Configure Vision 👁️", CallbackData: "settings_role:vision"},
			},
			{
				{Text: "Configure Audio 🎙️", CallbackData: "settings_role:audio"},
				{Text: "Configure Memory 💾", CallbackData: "settings_role:embed"},
			},
			{
				{Text: "Configure Learning 🧠", CallbackData: "settings_role:learn"},
			},
			{
				{Text: "Close ❌", CallbackData: "settings_role:close"},
			},
		},
	}
}

func (b *Bot) buildSettingsText() string {
	var sb strings.Builder
	sb.WriteString("⚙️ *OllamaBot Settings*\n\n")
	sb.WriteString("*Current Model Configuration:*\n")
	sb.WriteString(fmt.Sprintf("• 🤖 *Main (Default):* `%s`\n", b.cfg.OllamaDefaultModel))

	vis := b.cfg.OllamaModelVision
	if vis == "" {
		vis = "disabled 🚫"
	}
	sb.WriteString(fmt.Sprintf("• 👁️ *Vision:* `%s`\n", vis))

	aud := b.cfg.OllamaModelAudio
	if aud == "" {
		aud = "disabled 🚫"
	}
	sb.WriteString(fmt.Sprintf("• 🎙️ *Audio:* `%s`\n", aud))

	emb := b.cfg.OllamaModelEmbed
	if emb == "" {
		emb = "disabled 🚫"
	}
	sb.WriteString(fmt.Sprintf("• 💾 *Memory (Embed):* `%s`\n", emb))

	lrn := b.cfg.OllamaModelLearning
	if lrn == "" {
		lrn = "disabled 🚫"
	}
	sb.WriteString(fmt.Sprintf("• 🧠 *Learning:* `%s`\n", lrn))

	sb.WriteString("\nSelect a role below to configure its active model:")
	return sb.String()
}

func (b *Bot) handleSettingsRoleCallback(cb *CallbackQuery) {
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	role := strings.TrimPrefix(cb.Data, "settings_role:")

	if role == "close" {
		_ = b.editMessageText(chatID, msgID, "⚙️ *Settings Closed*", "Markdown", nil)
		_ = b.answerCallbackQuery(cb.ID, "Settings closed", false)
		return
	}

	if role == "menu" {
		text := b.buildSettingsText()
		markup := b.buildSettingsMarkup()
		_ = b.editMessageText(chatID, msgID, text, "Markdown", markup)
		_ = b.answerCallbackQuery(cb.ID, "", false)
		return
	}

	// Fetch models
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tags, err := b.client.Tags(ctx)
	if err != nil {
		_ = b.answerCallbackQuery(cb.ID, "❌ Ollama offline or error", true)
		return
	}

	var currentModel string
	switch role {
	case "main":
		currentModel = b.cfg.OllamaDefaultModel
	case "vision":
		currentModel = b.cfg.OllamaModelVision
	case "audio":
		currentModel = b.cfg.OllamaModelAudio
	case "embed":
		currentModel = b.cfg.OllamaModelEmbed
	case "learn":
		currentModel = b.cfg.OllamaModelLearning
	}
	if currentModel == "" {
		currentModel = "none"
	}

	var rows [][]InlineKeyboardButton
	for _, m := range tags.Models {
		displayName := m.Name
		// Mark current model
		if m.Name == currentModel {
			displayName = "🟢 " + displayName
		}
		callbackData := fmt.Sprintf("settings_model:%s:%s", role, m.Name)
		if len(callbackData) > 64 {
			// fallback: try using m.Model
			callbackData = fmt.Sprintf("settings_model:%s:%s", role, m.Model)
			if len(callbackData) > 64 {
				// skip or truncate
				continue
			}
		}
		rows = append(rows, []InlineKeyboardButton{
			{Text: displayName, CallbackData: callbackData},
		})
	}

	// Add "Disable model 🚫" button if not main role
	if role != "main" {
		rows = append(rows, []InlineKeyboardButton{
			{Text: "Disable model 🚫", CallbackData: fmt.Sprintf("settings_model:%s:none", role)},
		})
	}

	// Add Back button
	rows = append(rows, []InlineKeyboardButton{
		{Text: "⬅️ Back", CallbackData: "settings_role:menu"},
	})

	markup := &InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}

	var roleName string
	switch role {
	case "main":
		roleName = "Main"
	case "vision":
		roleName = "Vision"
	case "audio":
		roleName = "Audio"
	case "embed":
		roleName = "Memory (Embeddings)"
	case "learn":
		roleName = "Learning"
	}

	text := fmt.Sprintf("⚙️ *Configure Role:* %s\n\nSelect a model below for this capability:", roleName)
	_ = b.editMessageText(chatID, msgID, text, "Markdown", markup)
	_ = b.answerCallbackQuery(cb.ID, "", false)
}

func (b *Bot) handleSettingsModelCallback(cb *CallbackQuery) {
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	data := strings.TrimPrefix(cb.Data, "settings_model:")

	parts := strings.SplitN(data, ":", 2)
	if len(parts) < 2 {
		_ = b.answerCallbackQuery(cb.ID, "⚠️ Invalid callback data", true)
		return
	}
	role := parts[0]
	modelVal := parts[1]
	if modelVal == "none" {
		modelVal = ""
	}

	b.cfg.OllamaBaseURL = strings.TrimSpace(b.cfg.OllamaBaseURL)

	switch role {
	case "main":
		b.cfg.OllamaDefaultModel = modelVal
	case "vision":
		b.cfg.OllamaModelVision = modelVal
	case "audio":
		b.cfg.OllamaModelAudio = modelVal
	case "embed":
		b.cfg.OllamaModelEmbed = modelVal
	case "learn":
		b.cfg.OllamaModelLearning = modelVal
	}

	envPath := b.envPath
	if envPath == "" {
		envPath = ".env"
	}

	if err := config.SaveBasic(envPath, b.cfg); err != nil {
		log.Printf("[Telegram] Failed to save basic config: %v", err)
		_ = b.answerCallbackQuery(cb.ID, "❌ Failed to save config", true)
		return
	}

	_ = b.answerCallbackQuery(cb.ID, "✅ Configuration updated!", false)

	// Go back to main settings menu
	text := b.buildSettingsText()
	markup := b.buildSettingsMarkup()
	_ = b.editMessageText(chatID, msgID, text, "Markdown", markup)
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (b *Bot) registerCommands() {
	type BotCommand struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	type SetMyCommandsRequest struct {
		Commands []BotCommand `json:"commands"`
	}

	reqBody := SetMyCommandsRequest{
		Commands: []BotCommand{
			{Command: "start", Description: "Display welcome message"},
			{Command: "new", Description: "Start a new clean session"},
			{Command: "sessions", Description: "List recent sessions"},
			{Command: "session", Description: "Switch to a session by ID"},
			{Command: "status", Description: "Monitor VRAM and Ollama status"},
			{Command: "settings", Description: "Change active models configuration"},
			{Command: "projects", Description: "List autonomous workspace projects"},
			{Command: "memory", Description: "Query long-term semantic memory"},
			{Command: "reloadmodels", Description: "Force reload models inventory"},
			{Command: "goal", Description: "Manage persistent objective (start/pause/resume/clear/status)"},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("[Telegram] Failed to marshal commands: %v", err)
		return
	}

	url := b.apiBase + "/setMyCommands"
	resp, err := b.httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("[Telegram] Failed to register commands with Telegram: %v", err)
		return
	}
	defer resp.Body.Close()

	var apiResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err == nil {
		if apiResp.OK {
			log.Println("[Telegram] Commands successfully registered with Menu button")
		} else {
			log.Printf("[Telegram] Warning: Failed to register commands: %s", apiResp.Description)
		}
	}
}

func (b *Bot) notifyTaskCompletion(proj agent.Project, task agent.ProjectTodo, err error) {
	if len(b.cfg.TelegramAuthorizedIDs) == 0 {
		return
	}

	var sb strings.Builder
	if err != nil {
		sb.WriteString("❌ *Autonomous Task Failed!*\n\n")
		sb.WriteString(fmt.Sprintf("📂 *Project:* `%s`\n", proj.Name))
		sb.WriteString(fmt.Sprintf("🎯 *Goal:* %s\n", proj.Goal))
		sb.WriteString(fmt.Sprintf("📝 *Task:* %s\n", task.Content))
		sb.WriteString(fmt.Sprintf("⚠️ *Error:* %v\n", err))
	} else {
		if task.Status == "completed" {
			sb.WriteString("✅ *Autonomous Task Completed!*\n\n")
			sb.WriteString(fmt.Sprintf("📂 *Project:* `%s`\n", proj.Name))
			sb.WriteString(fmt.Sprintf("🎯 *Goal:* %s\n", proj.Goal))
			sb.WriteString(fmt.Sprintf("📝 *Task:* %s\n\n", task.Content))

			// Show execution result if present
			if strings.TrimSpace(task.Result) != "" {
				sb.WriteString("*Result:*\n")
				sb.WriteString(task.Result)
				sb.WriteString("\n\n")
			}

			if proj.Status == "completed" {
				sb.WriteString("🎉 *Project Fully Completed!*\n")
				sb.WriteString(fmt.Sprintf("All tasks for project `%s` have finished successfully.", proj.Name))
			}
		} else {
			return
		}
	}

	messageText := strings.TrimSpace(sb.String())
	chunks := splitMessage(messageText, 4000)

	for _, authID := range b.cfg.TelegramAuthorizedIDs {
		id, err := parseChatID(authID)
		if err == nil {
			for _, chunk := range chunks {
				_, _ = b.sendMessage(id, toTelegramHTML(chunk), 0, "HTML")
			}
		}
	}
}
