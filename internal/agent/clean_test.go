package agent

import "testing"

func TestCleanThinkingTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no tags", "Hello world", "Hello world"},
		{"think block", "<think>secret reasoning</think>Final answer", "Final answer"},
		{"thought block", "Before <thought>hmm</thought> after", "Before  after"},
		{"multiline block", "<think>\nline1\nline2\n</think>\nResult", "Result"},
		{"case insensitive", "<THINK>x</THINK>Answer", "Answer"},
		{"spaced tag", "< think >x</ think >Answer", "Answer"},
		{"stray open", "Answer <think> leftover", "Answer  leftover"},
		{"stray close", "Answer </thought>", "Answer"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanThinkingTokens(c.in); got != c.want {
				t.Fatalf("CleanThinkingTokens(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStreamThinkingFilter_Whole(t *testing.T) {
	var f StreamThinkingFilter
	out := f.Write("<think>reasoning</think>Hello")
	out += f.Flush()
	if out != "Hello" {
		t.Fatalf("got %q, want %q", out, "Hello")
	}
}

func TestStreamThinkingFilter_SplitTags(t *testing.T) {
	// Feed a thinking block split across many deltas, interleaved with real text.
	deltas := []string{"He", "llo ", "<th", "ink>hid", "den re", "ason", "ing</thi", "nk>", " world"}
	var f StreamThinkingFilter
	var out string
	for _, d := range deltas {
		out += f.Write(d)
	}
	out += f.Flush()
	if out != "Hello  world" {
		t.Fatalf("got %q, want %q", out, "Hello  world")
	}
}

func TestStreamThinkingFilter_NonThinkingAngle(t *testing.T) {
	// A '<' that is not a thinking tag must be preserved.
	var f StreamThinkingFilter
	var out string
	for _, d := range []string{"a < b and ", "c <div> d"} {
		out += f.Write(d)
	}
	out += f.Flush()
	if out != "a < b and c <div> d" {
		t.Fatalf("got %q, want %q", out, "a < b and c <div> d")
	}
}

func TestStreamThinkingFilter_OnlyThinking(t *testing.T) {
	var f StreamThinkingFilter
	out := f.Write("<think>only reasoning</think>")
	out += f.Flush()
	if out != "" {
		t.Fatalf("got %q, want empty", out)
	}
}

func TestStreamThinkingFilter_UnterminatedBlock(t *testing.T) {
	// An opening tag that never closes should swallow the rest on flush.
	var f StreamThinkingFilter
	out := f.Write("visible <think>tail that never closes")
	out += f.Flush()
	if out != "visible " {
		t.Fatalf("got %q, want %q", out, "visible ")
	}
}

func TestStreamRepetitionGuardDetectsAlternatingRows(t *testing.T) {
	var guard StreamRepetitionGuard
	rows := "| ku, tsu, su | tta | kaku → katta |\n| nu, mu, ru, bu, gu | nda | nomu → nonda |\n"
	for i := 0; i < 3; i++ {
		if guard.Observe(rows) {
			t.Fatalf("detected repetition after only %d cycles", i+1)
		}
	}
	if !guard.Observe(rows) {
		t.Fatal("expected repeated row cycle to be detected")
	}
}

func TestStreamRepetitionGuardAllowsNormalTable(t *testing.T) {
	var guard StreamRepetitionGuard
	text := "| Group | Dictionary | Negative | Past | Example |\n|---|---|---|---|---|\n| 1 | kaku | kakanai | kaita | I wrote |\n| 2 | taberu | tabenai | tabeta | I ate |\n| 3 | suru | shinai | shita | I did |\n"
	for _, chunk := range []string{text[:40], text[40:100], text[100:]} {
		if guard.Observe(chunk) {
			t.Fatal("normal table was incorrectly detected as repetitive")
		}
	}
}

func TestStripToolCallEnvelopes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no tags", "Hello world", "Hello world"},
		{"invoke envelope", `<invoke name="write_file">{"file_path":"a.txt","contents":"hello"}</invoke>`, ""},
		{"invoke with leading text", "Let me write that.\n\n<invoke name=\"write_file\">{\"file_path\":\"a.txt\"}</invoke>", "Let me write that."},
		{"tool_call envelope", `<tool_call name="edit_file">{"file_path":"b.go"}</tool_call>`, ""},
		{"custom tag envelope", `<write>{"file_path":"a.txt","contents":"hi"}</write>`, ""},
		{"custom tag case-insensitive", `<WRITE>{"file_path":"a.txt","contents":"hi"}</WRITE>`, ""},
		{"custom tag edit", `<edit>{"file_path":"b.go"}</edit>`, ""},
		{"mixed with text", "Before <invoke name=\"read_file\">{\"path\":\"x.go\"}</invoke> After", "Before  After"},
		{"unmapped custom tag preserved", "<div class=\"x\">content</div>", "<div class=\"x\">content</div>"},
		{"unmapped invoke-style preserved", "<invoke not a tool call>", "<invoke not a tool call>"},
		{"full tool tag not mapped to raw envelope", "<write_file>{\"file_path\":\"a.txt\"}</write_file>", "<write_file>{\"file_path\":\"a.txt\"}</write_file>"},
		{"multiple envelopes", "<invoke name=\"read_file\">{\"path\":\"a\"}</invoke>\n<invoke name=\"list_files\">{}</invoke>", ""},
		{"whitespace only", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripToolCallEnvelopes(c.in); got != c.want {
				t.Fatalf("StripToolCallEnvelopes(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
