package agent

import (
	"github.com/jonathanhecl/ollamabot/internal/ollama"
	"github.com/jonathanhecl/ollamabot/internal/sessions"
)

// recorderStreamHandler routes an agent stream into a session Recorder so the
// turn is persisted with its content, tool steps and metrics exactly like a
// web or Telegram turn, without any transport attached. Background goal and
// plan turns use it, and so does the device channel: its audio path only needs
// the recorder, never a live stream.
type recorderStreamHandler struct {
	recorder *sessions.Recorder
}

// NewRecorderStreamHandler builds a recorder-only stream handler.
func NewRecorderStreamHandler(recorder *sessions.Recorder) StreamHandler {
	return &recorderStreamHandler{recorder: recorder}
}

func (h *recorderStreamHandler) OnThinking(delta string) { h.recorder.OnThinking(delta) }
func (h *recorderStreamHandler) OnContent(delta string)  { h.recorder.OnContent(delta) }
func (h *recorderStreamHandler) OnToolCall(call ollama.ToolCall) {
	h.recorder.OnToolCall(call)
}
func (h *recorderStreamHandler) OnToolStart(name string, args any, source string) {
	h.recorder.OnToolStart(name, args, source)
}
func (h *recorderStreamHandler) OnToolResult(name, result string, source string) {
	h.recorder.OnToolResult(name, result, source)
}
func (h *recorderStreamHandler) OnMediaPreProcessing(content string) {
	h.recorder.OnMediaPreProcessing(content)
}
func (h *recorderStreamHandler) OnDone(resp ollama.ChatResponse) { h.recorder.OnDone(resp) }
func (h *recorderStreamHandler) OnEvent(kind string, data any) {
	h.recorder.RecordEvent(kind, sessions.EventContent(kind, data), data)
}
func (h *recorderStreamHandler) OnContextOptimizationStart(tokensBefore int, percentBefore float64) {
	h.recorder.OnContextOptimizationStart(tokensBefore, percentBefore)
}
func (h *recorderStreamHandler) OnContextOptimizationEnd(tokensAfter int, percentAfter float64, durationSeconds float64) {
	h.recorder.OnContextOptimizationEnd(tokensAfter, percentAfter, durationSeconds)
}
func (h *recorderStreamHandler) OnContextOptimized(optimizedMessages []ollama.Message, summary string, numKept int) {
	h.recorder.UpdateHistory(optimizedMessages, summary, numKept)
}
