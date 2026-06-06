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
