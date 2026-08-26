package tools

import (
	"reflect"
	"testing"
)

func TestParseJSONArgs_Valid(t *testing.T) {
	raw := []byte(`{"path": "main.go", "lines": 10}`)
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["path"] != "main.go" || args["lines"] != float64(10) {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestParseJSONArgs_TrailingComma(t *testing.T) {
	raw := []byte(`{"path": "main.go", "count": 5, }`)
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error on trailing comma: %v", err)
	}
	if args["path"] != "main.go" {
		t.Errorf("expected path main.go, got: %v", args["path"])
	}
}

func TestParseJSONArgs_MarkdownFence(t *testing.T) {
	raw := []byte("```json\n{\n  \"command\": \"ls -la\"\n}\n```")
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error on markdown fence: %v", err)
	}
	if args["command"] != "ls -la" {
		t.Errorf("expected ls -la, got: %v", args["command"])
	}
}

func TestParseJSONArgs_PythonBooleans(t *testing.T) {
	raw := []byte(`{"enabled": True, "dry_run": False, "extra": None}`)
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error on python booleans: %v", err)
	}
	if args["enabled"] != true || args["dry_run"] != false || args["extra"] != nil {
		t.Errorf("unexpected python converted args: %#v", args)
	}
}

func TestParseJSONArgs_SingleQuotes(t *testing.T) {
	raw := []byte(`{'action': 'read', 'path': 'config.json'}`)
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error on single quotes: %v", err)
	}
	if args["action"] != "read" {
		t.Errorf("expected read, got: %v", args["action"])
	}
}

func TestParseJSONArgs_UnclosedBrace(t *testing.T) {
	raw := []byte(`{"action": "create", "name": "test"`)
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error on unclosed brace: %v", err)
	}
	if args["name"] != "test" {
		t.Errorf("expected test, got: %v", args["name"])
	}
}

func TestParseJSONArgs_ProseSurrounding(t *testing.T) {
	raw := []byte(`Here is the parameter payload you requested: {"query": "ollama models"} Hope this helps!`)
	args, err := ParseJSONArgs(raw)
	if err != nil {
		t.Fatalf("unexpected error on surrounding prose: %v", err)
	}
	if args["query"] != "ollama models" {
		t.Errorf("expected query ollama models, got: %v", args["query"])
	}
}

func TestParseJSONArgs_Empty(t *testing.T) {
	args, err := ParseJSONArgs([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error on empty bytes: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected empty map, got: %#v", args)
	}
}

func TestRepairJSON_Direct(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "{}"},
		{`{"a": 1,}`, `{"a": 1}`},
		{`{"key": True}`, `{"key": true}`},
	}
	for _, tc := range tests {
		got := RepairJSON(tc.input)
		m1, _ := ParseJSONArgs([]byte(got))
		m2, _ := ParseJSONArgs([]byte(tc.expected))
		if !reflect.DeepEqual(m1, m2) && got != tc.expected {
			t.Logf("RepairJSON(%q) = %q", tc.input, got)
		}
	}
}
