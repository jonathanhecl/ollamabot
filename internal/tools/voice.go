package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// voiceMaxChars keeps a voice note in the range a person would actually listen
// to. Ollama accepts up to 4096 characters per request, but a spoken reply
// longer than about a minute stops being a better answer than text.
const voiceMaxChars = 1500

var (
	codeFencePattern = regexp.MustCompile("(?s)```.*?```")
	markdownLink     = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)
	bareURLPattern   = regexp.MustCompile(`https?://\S+`)
	tableRulePattern = regexp.MustCompile(`^\s*\|?[\s:|-]+\|[\s:|-]*$`)
	inlineCode       = regexp.MustCompile("`([^`]*)`")
	listMarker       = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	spaceRuns        = regexp.MustCompile(`\s+`)
)

// sendVoiceTool is the model-facing contract. The description carries the
// entire policy for when audio is appropriate, because the decision belongs to
// the model and the tool is the only place that can act on it.
func sendVoiceTool() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolDefinition{
			Name:        "send_voice",
			Description: "Deliver this reply as a spoken voice message instead of text. Use it only when the answer is short and conversational and hearing it is better than reading it: a greeting, a quick confirmation, a short explanation, a human-sounding reply. Never use it for code, file contents, tables, lists, step-by-step instructions, numbers, URLs, error output, or long answers; reply in text for those. The audio is delivered with its transcription, so do NOT repeat the spoken text in your reply. You may send several voice messages in one turn (for example, progress updates during a long task), but each call must say something NEW: never resend the same or nearly the same words. Call it at most once per model response.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": fmt.Sprintf("Exactly what should be spoken, written the way a person would say it, without Markdown, code, or emoji. This same text is shown as the transcription with the voice message, so write it as the complete reply. Maximum %d characters.", voiceMaxChars),
					},
				},
				"required": []string{"text"},
			},
		},
	}
}

// SetSpeechModel exposes the send_voice tool only while a speech model is
// configured. Removing the model removes the tool definition too, so the model
// never sees a capability the server cannot honor.
func (r *Registry) SetSpeechModel(model string) {
	r.speechModel = strings.TrimSpace(model)
	want := r.speechModel != ""

	if want && !r.enabled["send_voice"] {
		r.enabled["send_voice"] = true
		r.defs = append(r.defs, sendVoiceTool())
		return
	}
	if !want && r.enabled["send_voice"] {
		delete(r.enabled, "send_voice")
		filtered := r.defs[:0]
		for _, def := range r.defs {
			if def.Function.Name != "send_voice" {
				filtered = append(filtered, def)
			}
		}
		r.defs = filtered
	}
}

// VoiceEnabled reports whether send_voice is exposed to the model.
func (r *Registry) VoiceEnabled() bool {
	return r.enabled["send_voice"]
}

// SpeechModel returns the configured text-to-speech model, if any.
func (r *Registry) SpeechModel() string {
	return r.speechModel
}

// LastVoiceText returns the spoken text from the most recent successful
// send_voice call in this turn, or empty when no voice was generated yet.
func (r *Registry) LastVoiceText() string {
	return r.voiceSentText
}

// normalizeVoiceText reduces a voice text to its comparable essence:
// lowercase, no punctuation, single spaces. Two messages that differ only in
// capitalization or in "¡!¿?" placement normalize to the same string, which
// is exactly the near-duplicate bypass the per-turn guard must catch.
func normalizeVoiceText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, rn := range s {
		if unicode.IsLetter(rn) || unicode.IsNumber(rn) {
			b.WriteRune(rn)
		} else if unicode.IsSpace(rn) {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// voiceWordOverlap returns the Jaccard similarity (0..1) between the word sets
// of two normalized voice texts. Catches light paraphrases that are not
// string-equal but say the same thing (e.g. one added filler word).
func voiceWordOverlap(a, b string) float64 {
	setA := map[string]bool{}
	for _, w := range strings.Fields(a) {
		setA[w] = true
	}
	if len(setA) == 0 {
		return 0
	}
	setB := map[string]bool{}
	for _, w := range strings.Fields(b) {
		setB[w] = true
	}
	inter := 0
	for w := range setA {
		if setB[w] {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// voiceAlreadySent reports whether text was already spoken in this turn,
// either verbatim (after normalization) or nearly (high word overlap).
func (r *Registry) voiceAlreadySent(text string) bool {
	norm := normalizeVoiceText(text)
	if norm == "" {
		return false
	}
	for _, prev := range r.voiceSentTexts {
		prevNorm := normalizeVoiceText(prev)
		if prevNorm == "" {
			continue
		}
		if norm == prevNorm || voiceWordOverlap(norm, prevNorm) >= 0.9 {
			return true
		}
	}
	return false
}

func (r *Registry) sendVoice(ctx context.Context, args map[string]any) (string, error) {
	model := strings.TrimSpace(r.speechModel)
	if model == "" {
		return "", fmt.Errorf("no speech model configured - please set OLLAMA_MODEL_SPEECH in settings")
	}
	if r.client == nil {
		return "", fmt.Errorf("ollama client is required to synthesize speech")
	}
	if strings.TrimSpace(r.sessionID) == "" {
		return "", fmt.Errorf("voice replies require an active session")
	}
	if r.attachmentHandler == nil {
		return "", fmt.Errorf("this channel cannot deliver audio attachments; answer in text")
	}

	raw, _ := args["text"].(string)
	text := cleanSpeechText(raw)
	if text == "" {
		return "", fmt.Errorf("text is empty after removing Markdown and code; provide the words to speak")
	}
	if len(r.voiceSentTexts) > 0 && r.voiceAlreadySent(text) {
		return fmt.Sprintf("You already sent this voice message in this turn (same words, only punctuation or small details differ). Do NOT send it again: the user already heard it. Continue with your remaining work or end your turn."), nil
	}
	if chars := len([]rune(text)); chars > voiceMaxChars {
		return "", fmt.Errorf("text is too long for a voice note (%d characters, maximum %d); shorten it or answer in text", chars, voiceMaxChars)
	}

	// A cold speech model has to load before the first sample is produced, so
	// the registry raises this tool's timeout; keep a guard here too.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	res, err := r.client.Speech(ctx, ollama.SpeechRequest{
		Model:          model,
		Input:          text,
		ResponseFormat: ollama.SpeechFormatWAV,
	})
	if err != nil {
		return "", fmt.Errorf("speech synthesis failed: %w", err)
	}

	attDir := r.attachmentsDir()
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create attachments directory: %w", err)
	}
	filename := fmt.Sprintf("voice_%d.wav", time.Now().UnixNano())
	path := filepath.Join(attDir, filename)
	if err := os.WriteFile(path, res.Audio, 0o644); err != nil {
		return "", fmt.Errorf("failed to save voice note: %w", err)
	}

	mime := res.ContentType
	if strings.TrimSpace(mime) == "" {
		mime = "audio/wav"
	}
	r.attachmentHandler.OnAttachmentGenerated(r.sessionID, filename, mime, path, text)
	r.voiceSent = true
	r.voiceSentText = text
	r.voiceSentTexts = append(r.voiceSentTexts, text)

	return fmt.Sprintf("Voice note delivered to the user (%s, %d bytes) with its transcription. Do NOT repeat the spoken text in your reply. Continue with any remaining work; you may send another voice message later in this turn only if you have NEW words to say (never the same ones). End your turn when nothing is left to do.", filename, len(res.Audio)), nil
}

// attachmentsDir is the canonical session attachments location, matching
// generated images so the web and Telegram channels can serve the file.
func (r *Registry) attachmentsDir() string {
	if strings.TrimSpace(r.sessionsPath) != "" {
		return filepath.Join(r.sessionsPath, r.sessionID, "attachments")
	}
	return filepath.Join(r.workspace, "sessions", r.sessionID, "attachments")
}

// cleanSpeechText removes what a speech model would read literally or choke
// on: code blocks, links, bare URLs, tables, list markers and emphasis marks.
// Line breaks become spaces so the narration does not pause oddly.
func cleanSpeechText(input string) string {
	text := input

	text = codeFencePattern.ReplaceAllString(text, " ")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = bareURLPattern.ReplaceAllString(text, " ")

	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if tableRulePattern.MatchString(line) {
			continue
		}
		if strings.Count(line, "|") >= 2 {
			continue
		}
		kept = append(kept, line)
	}
	text = strings.Join(kept, "\n")

	text = inlineCode.ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")
	text = strings.ReplaceAll(text, "*", "")
	text = strings.ReplaceAll(text, "#", "")
	text = strings.ReplaceAll(text, ">", " ")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = listMarker.ReplaceAllString(line, "")
	}
	text = strings.Join(lines, "\n")

	text = spaceRuns.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
