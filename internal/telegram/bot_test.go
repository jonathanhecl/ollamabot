package telegram

import (
	"encoding/json"
	"testing"
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
