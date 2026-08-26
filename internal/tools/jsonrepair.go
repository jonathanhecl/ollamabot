package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	markdownFenceRegex   = regexp.MustCompile("(?s)^```(?:json)?\\s*(.*?)\\s*```$")
	trailingCommaRegex   = regexp.MustCompile(`,\s*([}\]])`)
	singleQuotePropRegex = regexp.MustCompile(`([{,]\s*)'([^']+)'\s*:`)
	unquotedKeyRegex     = regexp.MustCompile(`([{,]\s*)([a-zA-Z_][a-zA-Z0-9_]*)\s*:`)
)

// RepairJSON takes a potentially malformed or markdown-wrapped JSON string from a local LLM
// and applies heuristic fixes to produce valid JSON.
func RepairJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "{}"
	}

	// 1. Strip markdown code fences if present (```json ... ``` or ``` ... ```)
	if match := markdownFenceRegex.FindStringSubmatch(s); len(match) > 1 {
		s = strings.TrimSpace(match[1])
	}

	// 2. If surrounded by prose/commentary, extract outermost JSON object { ... } or array [ ... ]
	startObj := strings.IndexByte(s, '{')
	startArr := strings.IndexByte(s, '[')

	if startObj != -1 && (startArr == -1 || startObj < startArr) {
		// Target object
		lastObj := strings.LastIndexByte(s, '}')
		if lastObj != -1 && lastObj > startObj {
			s = s[startObj : lastObj+1]
		} else {
			s = s[startObj:]
		}
	} else if startArr != -1 {
		// Target array
		lastArr := strings.LastIndexByte(s, ']')
		if lastArr != -1 && lastArr > startArr {
			s = s[startArr : lastArr+1]
		} else {
			s = s[startArr:]
		}
	}

	// 3. Normalize Python literals: True -> true, False -> false, None -> null
	s = replacePythonLiterals(s)

	// 4. Convert single-quoted strings (both keys and values) to valid double-quoted JSON strings
	s = convertSingleQuotes(s)

	// 5. Fix unquoted keys if any: {key: "value"} -> {"key": "value"}
	s = unquotedKeyRegex.ReplaceAllString(s, `$1"$2":`)

	// 6. Fix trailing commas: { "a": 1, } -> { "a": 1 }
	s = trailingCommaRegex.ReplaceAllString(s, `$1`)

	// 7. Balance unclosed quotes and braces if response was truncated
	s = balanceJSON(s)

	return s
}

// convertSingleQuotes converts single-quoted JSON strings and property names to standard double-quoted strings.
func convertSingleQuotes(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inDouble := false
	inSingle := false
	escaped := false

	for i := 0; i < len(s); i++ {
		b := s[i]
		if inDouble {
			sb.WriteByte(b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inDouble = false
			}
			continue
		}

		if inSingle {
			if escaped {
				if b == '\'' {
					sb.WriteByte('\'')
				} else {
					sb.WriteByte('\\')
					sb.WriteByte(b)
				}
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '\'' {
				inSingle = false
				sb.WriteByte('"')
			} else if b == '"' {
				// Escape double quotes inside newly converted string
				sb.WriteString("\\\"")
			} else {
				sb.WriteByte(b)
			}
			continue
		}

		if b == '"' {
			inDouble = true
			sb.WriteByte(b)
			continue
		}

		if b == '\'' {
			inSingle = true
			sb.WriteByte('"')
			continue
		}

		sb.WriteByte(b)
	}

	return sb.String()
}

// replacePythonLiterals replaces Python booleans and None with JSON equivalents when outside strings.
func replacePythonLiterals(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inString := false
	var stringQuote byte
	escaped := false

	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ':' || r == '{' || r == '}' || r == '[' || r == ']'
	})
	_ = tokens

	for i := 0; i < len(s); i++ {
		b := s[i]
		if inString {
			sb.WriteByte(b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == stringQuote {
				inString = false
			}
			continue
		}

		if b == '"' || b == '\'' {
			inString = true
			stringQuote = b
			sb.WriteByte(b)
			continue
		}

		// Check for True / False / None words
		if strings.HasPrefix(s[i:], "True") && isWordBoundary(s, i, 4) {
			sb.WriteString("true")
			i += 3
			continue
		}
		if strings.HasPrefix(s[i:], "False") && isWordBoundary(s, i, 5) {
			sb.WriteString("false")
			i += 4
			continue
		}
		if strings.HasPrefix(s[i:], "None") && isWordBoundary(s, i, 4) {
			sb.WriteString("null")
			i += 3
			continue
		}

		sb.WriteByte(b)
	}

	return sb.String()
}

func isWordBoundary(s string, start int, length int) bool {
	end := start + length
	if start > 0 {
		prev := s[start-1]
		if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '_' {
			return false
		}
	}
	if end < len(s) {
		next := s[end]
		if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_' {
			return false
		}
	}
	return true
}

// balanceJSON balances unclosed double quotes and unmatched brackets/braces.
func balanceJSON(s string) string {
	inString := false
	escaped := false
	var stack []byte

	for i := 0; i < len(s); i++ {
		b := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		if b == '"' {
			inString = true
			continue
		}

		if b == '{' || b == '[' {
			stack = append(stack, b)
		} else if b == '}' {
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		} else if b == ']' {
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(s)

	if inString {
		sb.WriteByte('"')
	}

	// Close open brackets/braces in reverse order
	for i := len(stack) - 1; i >= 0; i-- {
		switch stack[i] {
		case '{':
			sb.WriteByte('}')
		case '[':
			sb.WriteByte(']')
		}
	}

	return sb.String()
}

// ParseJSONArgs attempts to unmarshal raw into a map[string]any. If standard unmarshaling
// fails, it applies RepairJSON to fix common LLM JSON syntax quirks and retries.
func ParseJSONArgs(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err == nil {
		if result == nil {
			return map[string]any{}, nil
		}
		return result, nil
	}

	// Try with string cleanup
	str := string(raw)
	repaired := RepairJSON(str)
	if err := json.Unmarshal([]byte(repaired), &result); err == nil {
		if result == nil {
			return map[string]any{}, nil
		}
		return result, nil
	}

	// Try extracting any single object if repaired still had issues
	start := strings.IndexByte(repaired, '{')
	end := strings.LastIndexByte(repaired, '}')
	if start != -1 && end > start {
		sub := repaired[start : end+1]
		if err := json.Unmarshal([]byte(sub), &result); err == nil {
			if result == nil {
				return map[string]any{}, nil
			}
			return result, nil
		}
	}

	return nil, fmt.Errorf("failed to parse JSON arguments: %s", str)
}
