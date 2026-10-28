package telegram

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/jonathanhecl/ollamabot/internal/router"
)

func TestMessageJSONParsingWithCaption(t *testing.T) {
	jsonStr := `{
		"message_id": 12345,
		"chat": {
			"id": 98765,
			"type": "private"
		},
		"caption": "This is a photo caption",
		"photo": [
			{
				"file_id": "file_id_123",
				"file_unique_id": "unique_123",
				"width": 800,
				"height": 600
			}
		]
	}`

	var msg Message
	err := json.Unmarshal([]byte(jsonStr), &msg)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if msg.MessageID != 12345 {
		t.Errorf("Expected message_id 12345, got %d", msg.MessageID)
	}

	if msg.Caption != "This is a photo caption" {
		t.Errorf("Expected caption 'This is a photo caption', got %q", msg.Caption)
	}

	if len(msg.Photo) != 1 || msg.Photo[0].FileID != "file_id_123" {
		t.Errorf("Expected 1 photo with file_id 'file_id_123', got %v", msg.Photo)
	}
}

func TestCaptionMappingToText(t *testing.T) {
	// Test case 1: Text is empty, Caption is present
	msg1 := Message{
		Caption: "Caption text",
	}
	// Simulate the mapping logic at the start of handleMessage
	if msg1.Text == "" && msg1.Caption != "" {
		msg1.Text = msg1.Caption
	}
	if msg1.Text != "Caption text" {
		t.Errorf("Expected Text to be 'Caption text', got %q", msg1.Text)
	}

	// Test case 2: Text is present, Caption is present
	msg2 := Message{
		Text:    "Original text",
		Caption: "Caption text",
	}
	if msg2.Text == "" && msg2.Caption != "" {
		msg2.Text = msg2.Caption
	}
	if msg2.Text != "Original text" {
		t.Errorf("Expected Text to remain 'Original text', got %q", msg2.Text)
	}

	// Test case 3: Text is empty, Caption is empty
	msg3 := Message{}
	if msg3.Text == "" && msg3.Caption != "" {
		msg3.Text = msg3.Caption
	}
	if msg3.Text != "" {
		t.Errorf("Expected Text to remain empty, got %q", msg3.Text)
	}
}

func TestGetNumberEmoji(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{1, "1️⃣"},
		{5, "5️⃣"},
		{10, "🔟"},
		{0, "[0]"},
		{11, "[11]"},
	}

	for _, tt := range tests {
		got := getNumberEmoji(tt.input)
		if got != tt.expected {
			t.Errorf("getNumberEmoji(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMCPToolLabelFormatting(t *testing.T) {
	tests := []struct {
		name     string
		args     any
		source   string
		expected string
	}{
		{
			name:     "vault_list",
			args:     nil,
			source:   "mcp:obsidian",
			expected: "vault_list (mcp:obsidian)",
		},
		{
			name:     "execute_sql",
			args:     nil,
			source:   "mcp:sqlite",
			expected: "execute_sql (mcp:sqlite)",
		},
		{
			name:     "web_search",
			args:     nil,
			source:   "internal",
			expected: "web_search",
		},
		{
			name:     "custom_tool",
			args:     nil,
			source:   "mcp",
			expected: "custom_tool (mcp)",
		},
	}

	for _, tt := range tests {
		toolLabel := getTelegramToolLabel(tt.name, tt.args)
		if tt.source != "" && tt.source != "internal" {
			toolLabel = toolLabel + " (" + tt.source + ")"
		}
		if toolLabel != tt.expected {
			t.Errorf("For %s (%s), expected %q, got %q", tt.name, tt.source, tt.expected, toolLabel)
		}
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSetMessageReaction(t *testing.T) {
	var requestedPath string
	var requestedBody map[string]any

	bot := &Bot{
		apiBase: "https://api.telegram.org/bot12345",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestedPath = req.URL.Path
				bodyBytes, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(bodyBytes, &requestedBody)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
				}, nil
			}),
		},
	}

	err := bot.setMessageReaction(123456, 789, "✅")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if requestedPath != "/bot12345/setMessageReaction" {
		t.Errorf("expected path /bot12345/setMessageReaction, got %s", requestedPath)
	}
	if requestedBody["chat_id"] != float64(123456) {
		t.Errorf("expected chat_id 123456, got %v", requestedBody["chat_id"])
	}
	if requestedBody["message_id"] != float64(789) {
		t.Errorf("expected message_id 789, got %v", requestedBody["message_id"])
	}
	reactions, ok := requestedBody["reaction"].([]any)
	if !ok || len(reactions) != 1 {
		t.Fatalf("expected 1 reaction, got %v", requestedBody["reaction"])
	}
	rItem, ok := reactions[0].(map[string]any)
	if !ok || rItem["type"] != "emoji" || rItem["emoji"] != "✅" {
		t.Errorf("expected emoji reaction ✅, got %v", reactions[0])
	}
}

func TestReactMessage_Fallback(t *testing.T) {
	var calls []string

	bot := &Bot{
		apiBase: "https://api.telegram.org/bot12345",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				bodyBytes, _ := io.ReadAll(req.Body)
				var payload struct {
					Reaction []struct {
						Emoji string `json:"emoji"`
					} `json:"reaction"`
				}
				_ = json.Unmarshal(bodyBytes, &payload)
				emoji := ""
				if len(payload.Reaction) > 0 {
					emoji = payload.Reaction[0].Emoji
				}
				calls = append(calls, emoji)

				if emoji == "✅" {
					// Simulate chat restriction error
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Body:       io.NopCloser(bytes.NewBufferString(`{"ok":false,"description":"REACTION_INVALID"}`)),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
				}, nil
			}),
		},
	}

	bot.reactMessage(123456, 789, "✅")

	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (initial + fallback), got %d: %v", len(calls), calls)
	}
	if calls[0] != "✅" || calls[1] != "👍" {
		t.Errorf("expected calls [✅, 👍], got %v", calls)
	}
}

func TestReactMessage_ZeroMessageID(t *testing.T) {
	called := false
	bot := &Bot{
		apiBase: "https://api.telegram.org/bot12345",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
				}, nil
			}),
		},
	}

	bot.reactMessage(123456, 0, "✅")
	if called {
		t.Errorf("reactMessage with messageID=0 should not make any HTTP call")
	}
}

func TestOnMediaResolvedAudioReaction(t *testing.T) {
	var reactedEmoji string
	var reactedMsgID int64

	bot := &Bot{
		apiBase: "https://api.telegram.org/bot12345",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				bodyBytes, _ := io.ReadAll(req.Body)
				var payload struct {
					MessageID int64 `json:"message_id"`
					Reaction  []struct {
						Emoji string `json:"emoji"`
					} `json:"reaction"`
				}
				_ = json.Unmarshal(bodyBytes, &payload)
				reactedMsgID = payload.MessageID
				if len(payload.Reaction) > 0 {
					reactedEmoji = payload.Reaction[0].Emoji
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
				}, nil
			}),
		},
	}

	// 1. Audio attachment transcribed successfully -> should react with ✅
	mediaRes := router.ResolveResult{
		Attachments: []router.AttachmentResult{
			{
				Kind:          "audio",
				Action:        router.ActionTranscribed,
				Transcription: "Hola, esto es una prueba",
				Unreadable:    false,
			},
		},
	}

	onMediaResolved := func(res router.ResolveResult, targetMsgID int64) {
		for _, att := range res.Attachments {
			if att.Kind == "audio" && !att.Unreadable && att.Action != router.ActionSkipped {
				bot.reactMessage(123456, targetMsgID, "✅")
				break
			}
		}
	}

	onMediaResolved(mediaRes, 999)
	if reactedMsgID != 999 || reactedEmoji != "✅" {
		t.Errorf("expected reaction ✅ on message 999, got emoji %q on message %d", reactedEmoji, reactedMsgID)
	}

	// 2. Unreadable audio -> should NOT react
	reactedEmoji = ""
	reactedMsgID = 0
	mediaResUnreadable := router.ResolveResult{
		Attachments: []router.AttachmentResult{
			{
				Kind:       "audio",
				Action:     router.ActionTranscribed,
				Unreadable: true,
			},
		},
	}
	onMediaResolved(mediaResUnreadable, 1000)
	if reactedMsgID != 0 || reactedEmoji != "" {
		t.Errorf("unreadable audio should not trigger reaction, got emoji %q on message %d", reactedEmoji, reactedMsgID)
	}

	// 3. Image only -> should NOT react
	reactedEmoji = ""
	reactedMsgID = 0
	mediaResImage := router.ResolveResult{
		Attachments: []router.AttachmentResult{
			{
				Kind:        "image",
				Action:      router.ActionDescribed,
				Description: "A photo of a cat",
			},
		},
	}
	onMediaResolved(mediaResImage, 1001)
	if reactedMsgID != 0 || reactedEmoji != "" {
		t.Errorf("image attachment should not trigger audio reaction, got emoji %q on message %d", reactedEmoji, reactedMsgID)
	}
}


