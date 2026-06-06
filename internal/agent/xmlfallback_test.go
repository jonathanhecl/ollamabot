package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseXMLFallback(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTool   string
		wantParams map[string]any
		wantOk     bool
	}{
		{
			name:       "simple invoke",
			input:      `<invoke name="Write">{"file_path": "a.txt", "contents": "hello"}</invoke>`,
			wantTool:   "Write",
			wantParams: map[string]any{"file_path": "a.txt", "contents": "hello"},
			wantOk:     true,
		},
		{
			name:       "simple tool_call",
			input:      `<tool_call name="Edit">{"file_path": "b.go", "old_string": "1"}</tool_call>`,
			wantTool:   "Edit",
			wantParams: map[string]any{"file_path": "b.go", "old_string": "1"},
			wantOk:     true,
		},
		{
			name:       "custom tag envelope",
			input:      `<READ_FILE>{"path": "README.md"}</READ_FILE>`,
			wantTool:   "ReadFile",
			wantParams: map[string]any{"path": "README.md"},
			wantOk:     true,
		},
		{
			name:       "trailing comma correction",
			input:      `<invoke name="Write">{"file_path": "c.txt", "contents": "comma",}</invoke>`,
			wantTool:   "Write",
			wantParams: map[string]any{"file_path": "c.txt", "contents": "comma"},
			wantOk:     true,
		},
		{
			name:  "no tag",
			input: `plain text response with no tags`,
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, params, ok := parseXMLFallback(tt.input)
			if ok != tt.wantOk {
				t.Errorf("parseXMLFallback() ok = %v, wantOk = %v", ok, tt.wantOk)
			}
			if ok {
				if tool != tt.wantTool {
					t.Errorf("parseXMLFallback() tool = %q, wantTool = %q", tool, tt.wantTool)
				}
				if !reflect.DeepEqual(params, tt.wantParams) {
					t.Errorf("parseXMLFallback() params = %v, wantParams = %v", params, tt.wantParams)
				}
			}
		})
	}
}

func TestDetectMalformedXMLFallback(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantMalformed bool
		wantSubstr    string
	}{
		{
			name:          "well-formed invoke",
			input:         `<invoke name="Write">{"file_path": "a.txt", "contents": "hello"}</invoke>`,
			wantMalformed: false,
		},
		{
			name:          "unbalanced braces invoke",
			input:         `<invoke name="Write">{"file_path": "a.txt"</invoke>`,
			wantMalformed: true,
			wantSubstr:    "Malformed JSON arguments in <invoke name=\"Write\">",
		},
		{
			name:          "invalid JSON syntax tool_call",
			input:         `<tool_call name="Edit">{"file_path": "a.txt", invalid}</tool_call>`,
			wantMalformed: true,
			wantSubstr:    "Invalid JSON syntax in <tool_call name=\"Edit\">",
		},
		{
			name:          "missing closing tag invoke",
			input:         `<invoke name="Write">{"file_path": "a.txt", "contents": "hello"}</invok>`,
			wantMalformed: true,
			wantSubstr:    "Missing closing tag </invoke> for tool Write.",
		},
		{
			name:          "custom tag unbalanced braces",
			input:         `<READ_FILE>{"path": "a.txt"</READ_FILE>`,
			wantMalformed: true,
			wantSubstr:    "Malformed JSON arguments in <READ_FILE>",
		},
		{
			name:          "custom tag missing close tag",
			input:         `<READ_FILE>{"path": "a.txt"}</READ_FIL>`,
			wantMalformed: true,
			wantSubstr:    "Missing closing tag </READ_FILE> for tool ReadFile.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMsg, gotMalformed := detectMalformedXMLFallback(tt.input)
			if gotMalformed != tt.wantMalformed {
				t.Errorf("detectMalformedXMLFallback() gotMalformed = %v, wantMalformed = %v", gotMalformed, tt.wantMalformed)
			}
			if gotMalformed && tt.wantSubstr != "" && !strings.Contains(gotMsg, tt.wantSubstr) {
				t.Errorf("detectMalformedXMLFallback() gotMsg = %q, wantSubstr = %q", gotMsg, tt.wantSubstr)
			}
		})
	}
}
