package tools

import (
	"context"
	"strings"
	"testing"
)

func TestSearchLive(t *testing.T) {
	res, err := Search(context.Background(), "golang news", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if res == "" {
		t.Error("expected non-empty search results")
	}
	if strings.Contains(strings.ToLower(res), "bot challenge") || strings.Contains(strings.ToLower(res), "captcha") {
		t.Errorf("search was blocked by bot challenge or captcha: %s", res)
	}
	t.Logf("Search results:\n%s", res)
}
