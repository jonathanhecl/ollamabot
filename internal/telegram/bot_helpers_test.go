package telegram

import (
	"strings"
	"testing"
)

func TestGetMimeType(t *testing.T) {
	tests := []struct {
		kind, name string
		expected   string
	}{
		{"audio", "test.wav", "audio/wav"},
		{"audio", "test.WAV", "audio/wav"},
		{"audio", "test.ogg", "audio/ogg"},
		{"audio", "test.mp3", "audio/ogg"}, // fallback for audio
		{"image", "test.png", "image/png"},
		{"image", "test.PNG", "image/png"},
		{"image", "test.jpg", "image/jpeg"},
		{"image", "test.jpeg", "image/jpeg"},
		{"other", "test.txt", "image/jpeg"}, // fallback for non-audio
	}

	for _, tt := range tests {
		got := getMimeType(tt.kind, tt.name)
		if got != tt.expected {
			t.Errorf("getMimeType(%q, %q) = %q; want %q", tt.kind, tt.name, got, tt.expected)
		}
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		text     string
		maxLen   int
		expected []string
	}{
		{"hello", 10, []string{"hello"}},
		{"hello\nworld", 6, []string{"hello", "world"}},
		{"hello world", 6, []string{"hello", "world"}},
		{"abcdefghij", 5, []string{"abcde", "fghij"}}, // split at maxLen because no space/newline
		{"abcde\n", 5, []string{"abcde"}},
	}

	for _, tt := range tests {
		got := splitMessage(tt.text, tt.maxLen)
		if len(got) != len(tt.expected) {
			t.Errorf("splitMessage(%q, %d) returned %d chunks, want %d: %v", tt.text, tt.maxLen, len(got), len(tt.expected), got)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("splitMessage(%q, %d)[%d] = %q; want %q", tt.text, tt.maxLen, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s        string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.s, tt.maxLen)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q; want %q", tt.s, tt.maxLen, got, tt.expected)
		}
	}
}

func TestSanitizeMathAndSimplifyLatex(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Block dollar math
		{"$$ \\frac{1}{2} $$", "1/2"},
		// Block bracket math
		{"\\[ \\sqrt{9} \\]", "√9"},
		// Inline dollar math
		{"$a \\times b$", "a × b"},
		// Inline parentheses math
		{"\\(a \\neq b\\)", "a ≠ b"},
		// Mix of math text
		{"The equation is $x^2 + y^2 = r^2$ and \\[ \\text{hello} \\]", "The equation is x^2 + y^2 = r^2 and hello"},
		// Various symbols
		{"$\\alpha \\beta \\gamma \\delta \\sigma \\theta \\div \\pm \\leq \\geq \\approx \\infty \\pi$", "α β γ δ σ θ ÷ ± ≤ ≥ ≈ ∞ π"},
		// Clean up left/right commands
		{"$\\left( x \\right)$", "( x )"},
		// Command removal
		{"$\\sum_{i=1}^{n} i$", "_i=1^n i"},
	}

	for _, tt := range tests {
		got := sanitizeMath(tt.input)
		// Strip multiple spaces or trim space to prevent minor spacing mismatch failures
		gotClean := strings.Join(strings.Fields(got), " ")
		expClean := strings.Join(strings.Fields(tt.expected), " ")
		if gotClean != expClean {
			t.Errorf("sanitizeMath(%q) = %q (clean: %q); want %q (clean: %q)", tt.input, got, gotClean, tt.expected, expClean)
		}
	}
}

func TestToTelegramHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Plain text", "Plain text"},
		{"**bold text**", "<b>bold text</b>"},
		{"_italic text_", "<i>italic text</i>"},
		{"[Google](https://google.com)", `<a href="https://google.com">Google</a>`},
		{"# Heading 1", "<b>Heading 1</b>"},
		{"## Heading 2", "<b>Heading 2</b>"},
		{"* Bullet item", "• Bullet item"},
		{"- Bullet item", "• Bullet item"},
		{"Escape & < > symbols", "Escape &amp; &lt; &gt; symbols"},
		{"Inline code `code & < >`", "Inline code <code>code &amp; &lt; &gt;</code>"},
		{"Code block:\n```go\nfmt.Println(\"<hello>\")\n```", "Code block:\n<pre><code>fmt.Println(\"&lt;hello&gt;\")\n</code></pre>"},
	}

	for _, tt := range tests {
		got := toTelegramHTML(tt.input)
		if got != tt.expected {
			t.Errorf("toTelegramHTML(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseChatID(t *testing.T) {
	tests := []struct {
		input       string
		expectedVal int64
		expectErr   bool
	}{
		{"12345", 12345, false},
		{"  -98765  ", -98765, false},
		{"abc", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		got, err := parseChatID(tt.input)
		if (err != nil) != tt.expectErr {
			t.Errorf("parseChatID(%q) error = %v; expectErr = %t", tt.input, err, tt.expectErr)
		}
		if got != tt.expectedVal {
			t.Errorf("parseChatID(%q) val = %d; want %d", tt.input, got, tt.expectedVal)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1024 * 1024, "1.00 MB"},
		{2048 * 1024 * 1024, "2.00 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %q; want %q", tt.bytes, got, tt.expected)
		}
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{2048, "2.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{2 * 1024 * 1024, "2.0 MB"},
	}

	for _, tt := range tests {
		got := humanSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("humanSize(%d) = %q; want %q", tt.bytes, got, tt.expected)
		}
	}
}
