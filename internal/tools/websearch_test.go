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

func TestSearchRich(t *testing.T) {
	res, err := Search(context.Background(), "weather in Buenos Aires", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if !strings.Contains(res, "URL:") {
		t.Errorf("expected clean URL formatting, got:\n%s", res)
	}
	if !strings.Contains(res, "Summary:") {
		t.Errorf("expected search result snippets (Summary), got:\n%s", res)
	}
	// Check that sponsored ad URLs are filtered out
	if strings.Contains(res, "y.js") || strings.Contains(res, "doubleclick") || strings.Contains(res, "aclick") {
		t.Errorf("found ad tracking links in results:\n%s", res)
	}
	t.Logf("Rich Search results:\n%s", res)
}

func TestFetchWebpage(t *testing.T) {
	res, err := Fetch(context.Background(), "https://go.dev/about/")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if res == "" {
		t.Error("expected non-empty fetch results")
	}
	if !strings.Contains(strings.ToLower(res), "go") {
		t.Errorf("expected fetched webpage to contain 'go', got:\n%s", res)
	}
	t.Logf("Fetched page sample (first 300 chars):\n%s", res[:300])
}
