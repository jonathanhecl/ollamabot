package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mockTelegramSender struct {
	sentMessages  []string
	sentDocuments []string
	sentPhotos    []string
	failMessage   bool
}

func (m *mockTelegramSender) SendMessage(ctx context.Context, apiBase string, chatID int64, text string) error {
	if m.failMessage {
		return fmt.Errorf("mock send message failure")
	}
	m.sentMessages = append(m.sentMessages, text)
	return nil
}

func (m *mockTelegramSender) SendDocument(ctx context.Context, apiBase string, chatID int64, filePath string, caption string) error {
	m.sentDocuments = append(m.sentDocuments, filepath.Base(filePath))
	return nil
}

func (m *mockTelegramSender) SendPhoto(ctx context.Context, apiBase string, chatID int64, filePath string, caption string) error {
	m.sentPhotos = append(m.sentPhotos, filepath.Base(filePath))
	return nil
}

func TestSendTelegram_TokenMissing(t *testing.T) {
	_, err := SendTelegram(context.Background(), "", 12345, nil, "Hello", nil, "", "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "token is not configured") {
		t.Fatalf("expected missing token error, got: %v", err)
	}
}

func TestSendTelegram_ChatIDResolution(t *testing.T) {
	mock := &mockTelegramSender{}
	// Resolution from authorizedIDs
	res, err := SendTelegram(context.Background(), "test-token", 0, []string{"987654321"}, "Hello from test", nil, "", "", "", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "987654321") {
		t.Errorf("expected result to mention chat ID 987654321, got: %s", res)
	}
	if len(mock.sentMessages) != 1 || mock.sentMessages[0] != "Hello from test" {
		t.Errorf("expected sent message 'Hello from test', got: %v", mock.sentMessages)
	}
}

func TestSendTelegram_MissingChatID(t *testing.T) {
	_, err := SendTelegram(context.Background(), "test-token", 0, nil, "Hello", nil, "", "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no Telegram chat_id provided") {
		t.Fatalf("expected missing chat_id error, got: %v", err)
	}
}

func TestSendTelegram_WithFiles(t *testing.T) {
	tempDir := t.TempDir()
	docFile := filepath.Join(tempDir, "report.pdf")
	imgFile := filepath.Join(tempDir, "chart.png")

	_ = os.WriteFile(docFile, []byte("PDF content"), 0644)
	_ = os.WriteFile(imgFile, []byte("PNG content"), 0644)

	mock := &mockTelegramSender{}
	res, err := SendTelegram(context.Background(), "test-token", 123456, nil, "Here is your report", []string{"report.pdf", "chart.png"}, tempDir, "", "", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "2 file(s)") {
		t.Errorf("expected result to mention 2 files, got: %s", res)
	}
	if len(mock.sentDocuments) != 1 || mock.sentDocuments[0] != "report.pdf" {
		t.Errorf("expected report.pdf in sentDocuments, got: %v", mock.sentDocuments)
	}
	if len(mock.sentPhotos) != 1 || mock.sentPhotos[0] != "chart.png" {
		t.Errorf("expected chart.png in sentPhotos, got: %v", mock.sentPhotos)
	}
}
