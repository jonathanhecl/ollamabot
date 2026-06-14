package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	openInvokeRe   = regexp.MustCompile(`(?is)<invoke[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>`)
	openToolCallRe = regexp.MustCompile(`(?is)<tool_call[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>`)
	toolTagOpenRe  = regexp.MustCompile(`(?s)<([A-Z][A-Z0-9_]*)>`)
)

var toolNameMapping = map[string]string{
	"readfile":               "read_file",
	"websearch":              "web_search",
	"fetchwebpage":           "fetch_webpage",
	"executecommand":         "execute_command",
	"askclarification":       "ask_clarification",
	"presentplan":            "present_plan",
	"generateimage":          "generate_image",
	"listsessionattachments": "list_session_attachments",
	"viewsessionattachment":  "view_session_attachment",
	"memorysearch":           "memory_search",
	"memoryadd":              "memory_add",
	"memorydelete":           "memory_delete",
	"memorylist":             "memory_list",
	"skilllist":              "skill_list",
	"skillget":               "skill_get",
	"skillcreate":            "skill_create",
	"skilledit":              "skill_edit",
	"skilldelete":            "skill_delete",
	"write":                  "Write",
	"edit":                   "Edit",
	"todowrite":              "TodoWrite",
}

// parseXMLFallback recovers a tool call from a raw model reply that did not use
// the native tool-call API. It accepts three envelopes:
//
//	<invoke name="ToolName">{...}</invoke>
//	<tool_call name="ToolName">{...}</tool_call>
//	<TOOLNAME>{...}</TOOLNAME>
func parseXMLFallback(text string) (string, map[string]any, bool) {
	clean := strings.TrimSpace(text)
	if !strings.Contains(clean, "</") {
		return "", nil, false
	}

	if name, params, ok := parseNamedEnvelope(clean, openInvokeRe, "</invoke>"); ok {
		return name, params, true
	}
	if name, params, ok := parseNamedEnvelope(clean, openToolCallRe, "</tool_call>"); ok {
		return name, params, true
	}
	if name, params, ok := parseToolTagEnvelope(clean); ok {
		return name, params, true
	}
	return "", nil, false
}

func parseNamedEnvelope(s string, openRe *regexp.Regexp, closeTag string) (string, map[string]any, bool) {
	loc := openRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return "", nil, false
	}
	name := strings.TrimSpace(s[loc[2]:loc[3]])
	norm := strings.ReplaceAll(strings.ToLower(name), "_", "")
	if mapped, ok := toolNameMapping[norm]; ok {
		name = mapped
	}
	body, end, ok := extractBalancedJSON(s, loc[1])
	if !ok {
		return "", nil, false
	}
	if !containsFold(s[end:], closeTag) {
		return "", nil, false
	}
	params, ok := decodeToolJSON(body)
	if !ok {
		return "", nil, false
	}
	return name, params, true
}

func parseToolTagEnvelope(s string) (string, map[string]any, bool) {
	loc := toolTagOpenRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return "", nil, false
	}
	tag := s[loc[2]:loc[3]]
	norm := strings.ReplaceAll(strings.ToLower(tag), "_", "")
	if _, ok := toolNameMapping[norm]; !ok {
		return "", nil, false
	}
	body, end, ok := extractBalancedJSON(s, loc[1])
	if !ok {
		return "", nil, false
	}
	closeTag := "</" + tag + ">"
	if !strings.Contains(s[end:], closeTag) {
		return "", nil, false
	}
	params, ok := decodeToolJSON(body)
	if !ok {
		return "", nil, false
	}
	return toToolName(tag), params, true
}

// extractBalancedJSON scans s starting at start, skips leading whitespace, and
// returns the first balanced JSON object as a substring along with the index
// just past the closing brace. It tracks string boundaries so braces inside string
// values are ignored.
func extractBalancedJSON(s string, start int) (string, int, bool) {
	i := start
	for i < len(s) && isJSONSpace(s[i]) {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", i, false
	}
	depth := 0
	inString := false
	escaped := false
	for j := i; j < len(s); j++ {
		c := s[j]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[i : j+1], j + 1, true
			}
		}
	}
	return "", len(s), false
}

func decodeToolJSON(raw string) (map[string]any, bool) {
	params := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &params); err == nil {
		return params, true
	}
	fixed := fixTrailingCommas(raw)
	if err := json.Unmarshal([]byte(fixed), &params); err == nil {
		return params, true
	}
	return nil, false
}

func toToolName(raw string) string {
	norm := strings.ReplaceAll(strings.ToLower(raw), "_", "")
	if mapped, ok := toolNameMapping[norm]; ok {
		return mapped
	}
	parts := strings.Split(strings.ToLower(raw), "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func fixTrailingCommas(in string) string {
	re := regexp.MustCompile(`,\s*([}\]])`)
	return re.ReplaceAllString(in, "$1")
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// detectMalformedXMLFallback checks if the text contains tool calls that are malformed
// (e.g. missing closing tags, unbalanced braces, or invalid JSON syntax).
// Returns a descriptive error message and true if a malformed call is detected.
func detectMalformedXMLFallback(text string) (string, bool) {
	clean := strings.TrimSpace(text)

	// 1. Check for <invoke ...> tags
	if strings.Contains(clean, "<invoke") {
		loc := openInvokeRe.FindStringSubmatchIndex(clean)
		if loc != nil {
			name := strings.TrimSpace(clean[loc[2]:loc[3]])
			norm := strings.ReplaceAll(strings.ToLower(name), "_", "")
			if mapped, ok := toolNameMapping[norm]; ok {
				name = mapped
			}
			body, _, ok := extractBalancedJSON(clean, loc[1])
			if !ok {
				return fmt.Sprintf("Error: Malformed JSON arguments in <invoke name=\"%s\">. Braces must be balanced.", name), true
			}
			_, ok = decodeToolJSON(body)
			if !ok {
				return fmt.Sprintf("Error: Invalid JSON syntax in <invoke name=\"%s\">: %s", name, body), true
			}
			if !containsFold(clean, "</invoke>") {
				return fmt.Sprintf("Error: Missing closing tag </invoke> for tool %s.", name), true
			}
		}
	}

	// 2. Check for <tool_call ...> tags
	if strings.Contains(clean, "<tool_call") {
		loc := openToolCallRe.FindStringSubmatchIndex(clean)
		if loc != nil {
			name := strings.TrimSpace(clean[loc[2]:loc[3]])
			norm := strings.ReplaceAll(strings.ToLower(name), "_", "")
			if mapped, ok := toolNameMapping[norm]; ok {
				name = mapped
			}
			body, _, ok := extractBalancedJSON(clean, loc[1])
			if !ok {
				return fmt.Sprintf("Error: Malformed JSON arguments in <tool_call name=\"%s\">. Braces must be balanced.", name), true
			}
			_, ok = decodeToolJSON(body)
			if !ok {
				return fmt.Sprintf("Error: Invalid JSON syntax in <tool_call name=\"%s\">: %s", name, body), true
			}
			if !containsFold(clean, "</tool_call>") {
				return fmt.Sprintf("Error: Missing closing tag </tool_call> for tool %s.", name), true
			}
		}
	}

	// 3. Check for custom tag envelopes like <READ_FILE>...</READ_FILE>
	loc := toolTagOpenRe.FindStringSubmatchIndex(clean)
	if loc != nil {
		tag := clean[loc[2]:loc[3]]
		norm := strings.ReplaceAll(strings.ToLower(tag), "_", "")
		if name, ok := toolNameMapping[norm]; ok {
			body, _, ok := extractBalancedJSON(clean, loc[1])
			if !ok {
				return fmt.Sprintf("Error: Malformed JSON arguments in <%s>. Braces must be balanced.", tag), true
			}
			_, ok = decodeToolJSON(body)
			if !ok {
				return fmt.Sprintf("Error: Invalid JSON syntax in <%s>: %s", tag, body), true
			}
			closeTag := "</" + tag + ">"
			if !strings.Contains(clean, closeTag) {
				return fmt.Sprintf("Error: Missing closing tag %s for tool %s.", closeTag, name), true
			}
		}
	}

	return "", false
}
