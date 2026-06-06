package agent

import (
	"regexp"
	"strings"
)

// thinkingTagNames are control/reasoning tags that some models leak into the
// final visible content instead of keeping them in the dedicated thinking field.
var thinkingTagNames = []string{"think", "thought", "thinking", "reasoning", "analysis", "reflection"}

var (
	// thinkingBlockPattern matches a complete thinking/control block leaked into content,
	// e.g. <think>...</think> or <thought>...</thought> (case-insensitive, multiline).
	thinkingBlockPattern = regexp.MustCompile(`(?is)<\s*(` + strings.Join(thinkingTagNames, "|") + `)\s*>.*?<\s*/\s*(` + strings.Join(thinkingTagNames, "|") + `)\s*>`)

	// strayThinkingTagPattern matches orphaned opening or closing thinking tags
	// that may remain after block removal (e.g. an unbalanced <think> or </think>).
	strayThinkingTagPattern = regexp.MustCompile(`(?is)<\s*/?\s*(` + strings.Join(thinkingTagNames, "|") + `)\s*>`)
)

// CleanThinkingTokens removes residual thinking/control tokens from a complete
// piece of text. It is intended to be applied to final assistant content before
// it is sent to the user (e.g. Telegram messages or persisted content).
func CleanThinkingTokens(text string) string {
	if text == "" {
		return text
	}
	cleaned := thinkingBlockPattern.ReplaceAllString(text, "")
	cleaned = strayThinkingTagPattern.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// StreamThinkingFilter incrementally strips residual thinking tags from streamed
// content. Because tags may be split across multiple deltas, the filter holds back
// any trailing text that could be the beginning of a thinking tag until it can
// safely decide whether to emit or discard it.
type StreamThinkingFilter struct {
	pending  strings.Builder // text that may be part of an incomplete tag
	skipping bool            // currently inside a thinking block
}

// Write feeds a streamed content delta into the filter and returns the portion of
// text that is safe to emit immediately (with thinking tags removed).
func (f *StreamThinkingFilter) Write(delta string) string {
	if delta == "" {
		return ""
	}
	f.pending.WriteString(delta)
	var out strings.Builder

	for {
		text := f.pending.String()

		if f.skipping {
			loc := strayThinkingTagPattern.FindStringIndex(text)
			if loc == nil {
				// Closing tag not fully received yet; keep buffering and discard nothing emittable.
				return out.String()
			}
			// Drop everything up to and including the closing tag, then continue scanning.
			f.pending.Reset()
			f.pending.WriteString(text[loc[1]:])
			f.skipping = false
			continue
		}

		idx := strings.IndexByte(text, '<')
		if idx == -1 {
			// No tag start at all; everything is safe to emit.
			out.WriteString(text)
			f.pending.Reset()
			return out.String()
		}

		// Emit text before the potential tag.
		out.WriteString(text[:idx])
		rest := text[idx:]

		if loc := thinkingBlockPattern.FindStringIndex(rest); loc != nil && loc[0] == 0 {
			// Complete thinking block at the start: drop it and continue.
			f.pending.Reset()
			f.pending.WriteString(rest[loc[1]:])
			continue
		}
		if loc := strayThinkingTagPattern.FindStringIndex(rest); loc != nil && loc[0] == 0 {
			// Opening thinking tag without a closing tag yet: enter skipping mode.
			f.pending.Reset()
			f.pending.WriteString(rest[loc[1]:])
			f.skipping = true
			continue
		}

		if couldBeThinkingTagPrefix(rest) {
			// Possibly an incomplete thinking tag: hold it back until more data arrives.
			f.pending.Reset()
			f.pending.WriteString(rest)
			return out.String()
		}

		// A '<' that is not (the start of) a thinking tag: emit it and keep scanning.
		out.WriteByte('<')
		f.pending.Reset()
		f.pending.WriteString(rest[1:])
	}
}

// Flush returns any text still held back, cleaned of thinking tokens. It should be
// called once streaming completes to emit any remaining buffered content.
func (f *StreamThinkingFilter) Flush() string {
	text := f.pending.String()
	f.pending.Reset()
	if f.skipping {
		f.skipping = false
		return ""
	}
	return CleanThinkingTokens(text)
}

// couldBeThinkingTagPrefix reports whether s (which must start with '<') could be
// the beginning of an as-yet-incomplete thinking tag, e.g. "<thi" or "</thoug".
func couldBeThinkingTagPrefix(s string) bool {
	if s == "" || s[0] != '<' {
		return false
	}
	// Build candidate openers/closers and check if s is a prefix of any of them.
	for _, name := range thinkingTagNames {
		open := "<" + name + ">"
		closeTag := "</" + name + ">"
		if isPrefixFold(s, open) || isPrefixFold(s, closeTag) {
			return true
		}
	}
	// Also treat the bare prefixes "<" and "</" as potential tag starts.
	return s == "<" || s == "</"
}

// isPrefixFold reports whether s is a case-insensitive prefix of full (and shorter
// than or equal to it), meaning s could grow into full.
func isPrefixFold(s, full string) bool {
	if len(s) > len(full) {
		return false
	}
	return strings.EqualFold(s, full[:len(s)])
}
