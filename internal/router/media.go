package router

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// MediaMessage extends ollama.Message with per-image kind metadata.
// ImageKinds[i] is "image" or "audio" for Images[i]. Transcriptions optionally
// carries previously extracted audio transcriptions (by audio order) so that
// history messages can be sanitized without re-processing media.
type MediaMessage struct {
	ollama.Message
	ImageKinds     []string `json:"image_kinds,omitempty"`
	Transcriptions []string `json:"-"`
}

// Attachment actions reported in AttachmentResult.Action.
const (
	ActionTranscribed = "transcribed" // audio transcribed by a dedicated model; text injected
	ActionDescribed   = "described"   // image described by a dedicated vision model
	ActionPassthrough = "passthrough" // raw media forwarded to the main model
	ActionSkipped     = "skipped"     // dropped: no model supports this media kind
)

// AttachmentResult is the structured, per-attachment outcome of media
// pre-processing. Index refers to the attachment's position in the original
// Images array of the user message.
type AttachmentResult struct {
	Index         int    `json:"index"`
	Kind          string `json:"kind"`
	Action        string `json:"action"`
	Model         string `json:"model,omitempty"`
	Transcription string `json:"transcription,omitempty"`
	Language      string `json:"language,omitempty"`
	Sounds        string `json:"sounds,omitempty"`
	Description   string `json:"description,omitempty"`
	Unreadable    bool   `json:"unreadable,omitempty"`
	Note          string `json:"note,omitempty"`
}

// ResolveResult is the outcome of ResolveMessages.
type ResolveResult struct {
	// Messages is the full conversation ready for the main model.
	Messages []ollama.Message
	// Attachments holds the per-attachment results for the last user message.
	Attachments []AttachmentResult
	// ContextNote is the synthetic assistant message injected before the user
	// message when dedicated models produced analyses ("" when none).
	ContextNote string
}

// ResolveMessages prepares a conversation for the main model:
//   - History (everything before the last user message) is sanitized: raw audio
//     base64 is never re-sent (its transcription text is kept instead), and
//     routed images are dropped (their description is already in history).
//   - The last user message's attachments are processed per Decide():
//     audio is ALWAYS transcribed (structured JSON), even in passthrough mode,
//     so the channel can display/persist the transcription; routed images are
//     described by the vision model with a prompt enriched by the user text and
//     the audio transcriptions.
func (r *Router) ResolveMessages(ctx context.Context, messages []MediaMessage) (ResolveResult, error) {
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	log.Printf("[Router] ResolveMessages: total=%d, lastUserIdx=%d", len(messages), lastUserIdx)

	var res ResolveResult
	out := make([]ollama.Message, 0, len(messages)+1)
	for i, msg := range messages {
		if msg.Role == "user" && i == lastUserIdx && len(msg.Images) > 0 {
			resolved, err := r.resolveActiveUserMessage(ctx, msg, &res)
			if err != nil {
				return res, err
			}
			out = append(out, resolved...)
			continue
		}
		if msg.Role == "user" && len(msg.Images) > 0 {
			out = append(out, r.sanitizeHistoryMessage(msg))
			continue
		}
		out = append(out, msg.Message)
	}
	res.Messages = out
	return res, nil
}

// sanitizeHistoryMessage strips media that must not be re-sent to the main
// model on follow-up turns: audio base64 is always dropped (huge and already
// transcribed) and images are dropped when image routing is active (their
// description already lives in the history as an assistant message).
func (r *Router) sanitizeHistoryMessage(msg MediaMessage) ollama.Message {
	dropImages := r.Decide("image") != DecisionPassthrough
	var kept []string
	audioCount := 0
	for i, b64 := range msg.Images {
		kind := "image"
		if i < len(msg.ImageKinds) {
			kind = msg.ImageKinds[i]
		}
		if kind == "audio" {
			audioCount++
			continue
		}
		if dropImages {
			continue
		}
		kept = append(kept, b64)
	}

	sanitized := msg.Message
	sanitized.Images = kept
	if audioCount > 0 && strings.TrimSpace(sanitized.Content) == "" {
		var parts []string
		for _, t := range msg.Transcriptions {
			if strings.TrimSpace(t) != "" {
				parts = append(parts, fmt.Sprintf("[Audio transcription]: %q", t))
			}
		}
		if len(parts) > 0 {
			sanitized.Content = strings.Join(parts, "\n")
		} else {
			sanitized.Content = "[The user sent an audio message]"
		}
	}
	if len(msg.Images) != len(kept) {
		log.Printf("[Router] sanitizeHistoryMessage: images %d → %d (audio dropped=%d, dropImages=%v)", len(msg.Images), len(kept), audioCount, dropImages)
	}
	return sanitized
}

// resolveActiveUserMessage processes the attachments of the latest user
// message and returns the message(s) to append: an optional assistant context
// message followed by the resolved user message.
func (r *Router) resolveActiveUserMessage(ctx context.Context, msg MediaMessage, res *ResolveResult) ([]ollama.Message, error) {
	type attachment struct {
		index  int
		kind   string
		base64 string
	}
	var attachments []attachment
	for i, b64 := range msg.Images {
		kind := "image"
		if i < len(msg.ImageKinds) {
			kind = msg.ImageKinds[i]
		}
		attachments = append(attachments, attachment{index: i, kind: kind, base64: b64})
	}
	log.Printf("[Router] resolveActiveUserMessage: %d attachment(s), kinds=%v, content_len=%d", len(attachments), msg.ImageKinds, len(msg.Content))

	var passthrough []string
	var injectedTranscriptions []string
	var imageAnalyses []string
	var userNotes []string
	totalAudio := 0
	for _, att := range attachments {
		if att.kind == "audio" {
			totalAudio++
		}
	}

	// Pass 1: audio. Transcription is ALWAYS extracted (structured output),
	// even in passthrough mode, so the channel can display and persist it.
	audioSeq := 0
	for _, att := range attachments {
		if att.kind != "audio" {
			continue
		}
		audioSeq++
		label := "the audio message"
		if totalAudio > 1 {
			label = fmt.Sprintf("audio attachment %d of %d", audioSeq, totalAudio)
		}
		result := AttachmentResult{Index: att.index, Kind: "audio"}
		if strings.TrimSpace(att.base64) == "" {
			result.Action = ActionSkipped
			result.Note = "empty audio data"
			res.Attachments = append(res.Attachments, result)
			continue
		}

		decision := r.Decide("audio")
		switch decision {
		case DecisionUnsupported:
			result.Action = ActionSkipped
			result.Note = "no configured model supports audio"
			userNotes = append(userNotes, fmt.Sprintf("[%s was ignored: no configured model supports audio]", capitalize(label)))
		case DecisionRoute, DecisionPassthrough:
			t, err := r.TranscribeAudio(ctx, att.base64)
			if err != nil {
				log.Printf("[Router] resolveActiveUserMessage: transcription failed: %v", err)
				result.Unreadable = true
				result.Note = err.Error()
				t = AudioTranscription{Unreadable: true}
			} else {
				result.Transcription = t.Transcription
				result.Language = t.Language
				result.Sounds = t.Sounds
				result.Unreadable = t.Unreadable
			}
			result.Model = r.audioModel()
			// The transcription text is injected in BOTH modes: even models
			// with native audio sometimes fail to attend the attachment when
			// tools/system prompts are present, so the transcription keeps the
			// behavior deterministic. In passthrough mode the raw audio is
			// also forwarded so the main model can analyze tone, sounds, etc.
			injectedTranscriptions = append(injectedTranscriptions, transcriptionBlock(label, t))
			if decision == DecisionPassthrough {
				result.Action = ActionPassthrough
				passthrough = append(passthrough, att.base64)
			} else {
				result.Action = ActionTranscribed
			}
		}
		res.Attachments = append(res.Attachments, result)
	}

	// Build the image analysis prompt: user text enriched with the routed
	// audio transcriptions (the audio may carry the instructions).
	imagePrompt := strings.TrimSpace(msg.Content)
	if len(injectedTranscriptions) > 0 {
		combined := strings.Join(injectedTranscriptions, "\n")
		if imagePrompt != "" {
			imagePrompt = fmt.Sprintf("%s\n\n[Instructions/context transcribed from the user's audio]:\n%s", imagePrompt, combined)
		} else {
			imagePrompt = fmt.Sprintf("Analyze this image following the instructions transcribed from the user's audio:\n%s", combined)
		}
	}

	// Pass 2: images.
	imageSeq := 0
	for _, att := range attachments {
		if att.kind == "audio" {
			continue
		}
		imageSeq++
		result := AttachmentResult{Index: att.index, Kind: "image"}
		switch r.Decide("image") {
		case DecisionUnsupported:
			result.Action = ActionSkipped
			result.Note = "no configured model supports images"
			userNotes = append(userNotes, "[An image attachment was ignored: no configured model supports images]")
		case DecisionPassthrough:
			result.Action = ActionPassthrough
			passthrough = append(passthrough, att.base64)
		case DecisionRoute:
			analysis, err := r.AnalyzeImage(ctx, att.base64, imagePrompt)
			if err != nil {
				log.Printf("[Router] resolveActiveUserMessage: image analysis failed: %v", err)
				result.Action = ActionSkipped
				result.Note = err.Error()
				userNotes = append(userNotes, "[An image attachment could not be analyzed]")
				break
			}
			result.Action = ActionDescribed
			result.Model = r.visionModel()
			result.Description = analysis
			imageAnalyses = append(imageAnalyses, fmt.Sprintf("[Image %d analysis by %s]:\n%s", imageSeq, r.visionModel(), analysis))
		}
		res.Attachments = append(res.Attachments, result)
	}

	var out []ollama.Message
	if len(imageAnalyses) > 0 {
		res.ContextNote = "Media pre-processing context (produced by a vision model, not written by the user):\n\n" + strings.Join(imageAnalyses, "\n\n")
		out = append(out, ollama.Message{Role: "assistant", Content: res.ContextNote})
	}

	resolved := msg.Message
	resolved.Images = passthrough

	var contentParts []string
	if strings.TrimSpace(resolved.Content) != "" {
		contentParts = append(contentParts, resolved.Content)
	}
	contentParts = append(contentParts, injectedTranscriptions...)
	contentParts = append(contentParts, userNotes...)
	resolved.Content = strings.Join(contentParts, "\n\n")

	if strings.TrimSpace(resolved.Content) == "" {
		if len(passthrough) > 0 {
			resolved.Content = "Respond to the attached media."
		} else if len(imageAnalyses) > 0 {
			resolved.Content = "Respond to the attached media analysis."
		}
	}

	log.Printf("[Router] resolveActiveUserMessage: resolved content_len=%d, passthrough=%d, results=%d", len(resolved.Content), len(passthrough), len(res.Attachments))
	out = append(out, resolved)
	return out, nil
}

// transcriptionBlock formats a routed audio transcription for injection into
// the user message content sent to the main model.
func transcriptionBlock(label string, t AudioTranscription) string {
	if t.Unreadable && strings.TrimSpace(t.Transcription) == "" {
		return fmt.Sprintf("[%s could not be transcribed]", capitalize(label))
	}
	if strings.TrimSpace(t.Transcription) == "" && strings.TrimSpace(t.Sounds) != "" {
		return fmt.Sprintf("[%s contains no speech. Sounds: %s]", capitalize(label), t.Sounds)
	}
	block := fmt.Sprintf("[Transcription of %s]: %q", label, t.Transcription)
	if strings.TrimSpace(t.Sounds) != "" {
		block += fmt.Sprintf(" (non-speech sounds: %s)", t.Sounds)
	}
	return block
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
