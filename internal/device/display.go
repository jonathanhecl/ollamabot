package device

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	fencedCode   = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\\n?(.*?)```")
	inlineCode   = regexp.MustCompile("`([^`]*)`")
	imageSyntax  = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkSyntax   = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	headingMarks = regexp.MustCompile(`(?m)^#{1,6}\s*`)
	quoteMarks   = regexp.MustCompile(`(?m)^>\s?`)
	listMarks    = regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	emphasis     = regexp.MustCompile(`[*_~]{1,3}`)
	tablePipes   = regexp.MustCompile(`\|`)
	spaces       = regexp.MustCompile(`[ \t]+`)
	blankLines   = regexp.MustCompile(`\n{3,}`)
)

// screenText reduces a markdown answer to plain text for the device's small
// screen, then truncates it on a word boundary.
func screenText(markdown string, limit int) string {
	text := fencedCode.ReplaceAllString(markdown, "$1")
	text = imageSyntax.ReplaceAllString(text, "$1")
	text = linkSyntax.ReplaceAllString(text, "$1")
	text = inlineCode.ReplaceAllString(text, "$1")
	text = headingMarks.ReplaceAllString(text, "")
	text = quoteMarks.ReplaceAllString(text, "")
	text = listMarks.ReplaceAllString(text, "• ")
	text = emphasis.ReplaceAllString(text, "")
	text = tablePipes.ReplaceAllString(text, " ")
	text = spaces.ReplaceAllString(text, " ")
	text = blankLines.ReplaceAllString(text, "\n\n")
	return truncateRunes(strings.TrimSpace(text), limit)
}

// truncateRunes cuts text to at most limit runes, ending at a word boundary
// with an ellipsis when the text was longer.
func truncateRunes(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	cut := runes[:limit]
	trimmed := strings.TrimRight(string(cut), " \t\n")
	if idx := strings.LastIndexAny(trimmed, " \t\n"); idx > limit/2 {
		trimmed = trimmed[:idx]
	}
	return strings.TrimRight(trimmed, " \t\n,.;:") + "…"
}
