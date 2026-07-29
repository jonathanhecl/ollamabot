package agent

import (
	"regexp"
	"strings"
)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// normalizeForURLMatch strips scheme, www prefix, trailing slashes and
// punctuation so a search query can be compared against a user-provided URL.
func normalizeForURLMatch(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimRight(s, "/.,;:!?)]")
	return s
}

// redirectSearchToFetch detects when a web_search query merely repeats a URL
// the user already provided in their goal. In that case the correct action is
// to fetch that URL directly instead of searching for it.
func redirectSearchToFetch(goal, query string) (string, bool) {
	if goal == "" || query == "" {
		return "", false
	}
	normQuery := normalizeForURLMatch(query)
	if normQuery == "" {
		return "", false
	}
	for _, raw := range urlPattern.FindAllString(goal, -1) {
		normURL := normalizeForURLMatch(raw)
		if normURL == "" {
			continue
		}
		if normQuery == normURL ||
			strings.Contains(normQuery, normURL) ||
			(len(normURL) >= len(normQuery) && strings.Contains(normURL, normQuery)) {
			return strings.TrimRight(raw, "/.,;:!?)]"), true
		}
	}
	return "", false
}
