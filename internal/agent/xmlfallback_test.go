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
			input:      `<invoke name="write_file">{"file_path": "a.txt", "contents": "hello"}</invoke>`,
			wantTool:   "write_file",
			wantParams: map[string]any{"file_path": "a.txt", "contents": "hello"},
			wantOk:     true,
		},
		{
			name:       "simple tool_call",
			input:      `<tool_call name="edit_file">{"file_path": "b.go", "old_string": "1"}</tool_call>`,
			wantTool:   "edit_file",
			wantParams: map[string]any{"file_path": "b.go", "old_string": "1"},
			wantOk:     true,
		},
		{
			name:       "custom tag envelope mapping",
			input:      `<READ_FILE>{"path": "README.md"}</READ_FILE>`,
			wantTool:   "read_file",
			wantParams: map[string]any{"path": "README.md"},
			wantOk:     true,
		},
		{
			name:       "custom tag web_search mapping",
			input:      `<WEB_SEARCH>{"query": "golang"}</WEB_SEARCH>`,
			wantTool:   "web_search",
			wantParams: map[string]any{"query": "golang"},
			wantOk:     true,
		},
		{
			name:       "lowercase custom tag execute command mapping",
			input:      `<execute_command>{"command": "python3", "args": ["test_extraction_script.py"]}</execute_command>`,
			wantTool:   "execute_command",
			wantParams: map[string]any{"command": "python3", "args": []any{"test_extraction_script.py"}},
			wantOk:     true,
		},
		{
			name:       "invoke name CamelCase ReadFile mapping",
			input:      `<invoke name="ReadFile">{"path": "a.txt"}</invoke>`,
			wantTool:   "read_file",
			wantParams: map[string]any{"path": "a.txt"},
			wantOk:     true,
		},
		{
			name:       "trailing comma correction",
			input:      `<invoke name="write_file">{"file_path": "c.txt", "contents": "comma",}</invoke>`,
			wantTool:   "write_file",
			wantParams: map[string]any{"file_path": "c.txt", "contents": "comma"},
			wantOk:     true,
		},
		{
			name:   "no tag",
			input:  `plain text response with no tags`,
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
			input:         `<invoke name="write_file">{"file_path": "a.txt", "contents": "hello"}</invoke>`,
			wantMalformed: false,
		},
		{
			name:          "unbalanced braces invoke",
			input:         `<invoke name="write_file">{"file_path": "a.txt"</invoke>`,
			wantMalformed: true,
			wantSubstr:    "Malformed JSON arguments in <invoke name=\"write_file\">",
		},
		{
			name:          "invalid JSON syntax tool_call",
			input:         `<tool_call name="edit_file">{"file_path": "a.txt", invalid}</tool_call>`,
			wantMalformed: true,
			wantSubstr:    "Invalid JSON syntax in <tool_call name=\"edit_file\">",
		},
		{
			name:          "missing closing tag invoke",
			input:         `<invoke name="write_file">{"file_path": "a.txt", "contents": "hello"}</invok>`,
			wantMalformed: true,
			wantSubstr:    "Missing closing tag </invoke> for tool write_file.",
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
			wantSubstr:    "Missing closing tag </READ_FILE> for tool read_file.",
		},
		{
			name:          "HTML tags ignored in text",
			input:         `This is <HTML> and <DIV> content with no JSON.`,
			wantMalformed: false,
		},
		{
			name:          "lowercase HTML tags ignored in text",
			input:         `Here is some <div> content.`,
			wantMalformed: false,
		},
		{
			name:          "uppercase doc placeholder ignored",
			input:         `Write the code to <FILE_PATH> now.`,
			wantMalformed: false,
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
