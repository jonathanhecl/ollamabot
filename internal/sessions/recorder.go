package sessions

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

// isInjectedContextMessage reports whether a system message was injected at
// request time for the agent (attachments/uploads context) and must not be
// persisted in the session timeline.
func isInjectedContextMessage(content string) bool {
	return strings.Contains(content, "The current session contains the following attachments") ||
		strings.Contains(content, "The user has uploaded the following files to this session")
}

// TurnSnapshot holds the steps and metrics for one assistant turn.
type TurnSnapshot struct {
	Steps   []Step
	Metrics Metrics
}

// Recorder persists the agent stream into the canonical session timeline.
type Recorder struct {
	store     *Store
	sessionID string
	model     string
	channel   string

	mu             sync.Mutex
	baseHistory    []RawMsg
	activeMessages []RawMsg
	turnEnded      bool
	currentTurn    TurnSnapshot
	turns          []TurnSnapshot
	lastNotifyTime time.Time
	saveGen        uint64
}

func NewRecorder(store *Store, sessionID string, baseHistory []RawMsg, model string, channel string) *Recorder {
	copied := make([]RawMsg, len(baseHistory))
	copy(copied, baseHistory)
	return &Recorder{
		store:       store,
		sessionID:   sessionID,
		model:       model,
		channel:     channel,
		baseHistory: copied,
	}
}

func AppendThinkingStep(steps []Step, delta string) []Step {
	if delta == "" {
		return steps
	}
	if n := len(steps); n > 0 && steps[n-1].Type == "thinking" {
		steps[n-1].Content += delta
		return steps
	}
	return append(steps, Step{Type: "thinking", Content: delta})
}

func FinalizeSteps(steps []Step) []Step {
	if len(steps) == 0 {
		return nil
	}
	out := make([]Step, len(steps))
	for i, step := range steps {
		out[i] = step
		switch {
		case step.Type == "image_progress" && strings.TrimSpace(step.ImageURL) != "":
			out[i].Status = "done"
			if strings.TrimSpace(out[i].Content) == "" || strings.Contains(strings.ToLower(out[i].Content), "generating image") {
				out[i].Content = "Image generated!"
			}
		case step.Type == "image_progress" && step.Status == "error":
			out[i].Status = "error"
		case step.Type == "image_progress":
			out[i].Status = "running"
		default:
			out[i].Status = ""
		}
	}
	return out
}

func generationURL(sessionID, filename string) string {
	sessionID = strings.TrimSpace(sessionID)
	filename = strings.TrimSpace(filename)
	if sessionID == "" || filename == "" {
		return ""
	}
	return "/api/sessions/" + sessionID + "/generations/" + filename
}

func generatedAttachmentNames(attachments []AttachmentMeta) []string {
	out := make([]string, 0, len(attachments))
	for _, att := range attachments {
		if att.Kind != "image" {
			continue
		}
		name := strings.TrimSpace(att.Name)
		if name == "" || !strings.HasPrefix(name, "generated_") {
			continue
		}
		out = append(out, name)
	}
	return out
}

// ResolveImageProgressSteps links completed generated attachments to image_progress steps
// and removes stale in-progress placeholders once a final image is known.
func ResolveImageProgressSteps(steps []Step, attachments []AttachmentMeta, sessionID string) []Step {
	if len(steps) == 0 {
		return steps
	}
	generated := generatedAttachmentNames(attachments)
	used := make(map[string]struct{}, len(generated))
	out := make([]Step, 0, len(steps))

	for _, step := range steps {
		if step.Type != "image_progress" {
			out = append(out, step)
			continue
		}
		if strings.TrimSpace(step.ImageURL) == "" {
			for _, name := range generated {
				if _, ok := used[name]; ok {
					continue
				}
				step.ImageURL = generationURL(sessionID, name)
				used[name] = struct{}{}
				break
			}
		} else {
			if name := filepathBaseFromGenerationURL(step.ImageURL); name != "" {
				used[name] = struct{}{}
			}
		}
		if strings.TrimSpace(step.ImageURL) != "" {
			step.Status = "done"
			if strings.TrimSpace(step.Content) == "" || strings.Contains(strings.ToLower(step.Content), "generating image") {
				step.Content = "Image generated!"
			}
		}
		out = append(out, step)
	}

	hasDoneImage := false
	for _, step := range out {
		if step.Type == "image_progress" && strings.TrimSpace(step.ImageURL) != "" {
			hasDoneImage = true
			break
		}
	}
	if !hasDoneImage {
		return out
	}
	filtered := make([]Step, 0, len(out))
	for _, step := range out {
		if step.Type == "image_progress" && strings.TrimSpace(step.ImageURL) == "" {
			continue
		}
		filtered = append(filtered, step)
	}
	return filtered
}

func filepathBaseFromGenerationURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 && idx+1 < len(rawURL) {
		return rawURL[idx+1:]
	}
	return rawURL
}

func FinalizeStepsWithThinking(steps []Step, thinking string) []Step {
	hasThinking := false
	for _, step := range steps {
		if step.Type == "thinking" {
			hasThinking = true
			break
		}
	}
	if thinking != "" && !hasThinking {
		steps = append([]Step{{Type: "thinking", Content: thinking}}, steps...)
	}
	return FinalizeSteps(steps)
}

func (r *Recorder) startNewTurnIfNeeded() {
	if !r.turnEnded {
		return
	}
	r.turns = append(r.turns, r.currentTurn)
	r.currentTurn = TurnSnapshot{}
	r.turnEnded = false
	r.activeMessages = append(r.activeMessages, RawMsg{
		Role:      "assistant",
		Timestamp: time.Now().Format(time.RFC3339),
		Model:     r.model,
		Channel:   r.channel,
	})
}

func (r *Recorder) getOrCreateAssistantMsg() *RawMsg {
	r.startNewTurnIfNeeded()
	return r.getOrCreateCurrentAssistantMsg()
}

func (r *Recorder) getOrCreateCurrentAssistantMsg() *RawMsg {
	if len(r.activeMessages) == 0 || r.activeMessages[len(r.activeMessages)-1].Role != "assistant" {
		r.activeMessages = append(r.activeMessages, RawMsg{
			Role:      "assistant",
			Timestamp: time.Now().Format(time.RFC3339),
			Model:     r.model,
			Channel:   r.channel,
		})
	}
	msg := &r.activeMessages[len(r.activeMessages)-1]
	msg.Timestamp = time.Now().Format(time.RFC3339)
	msg.Model = r.model
	msg.Channel = r.channel
	return msg
}

func (r *Recorder) OnThinking(delta string) {
	r.mu.Lock()
	r.getOrCreateAssistantMsg()
	r.currentTurn.Steps = AppendThinkingStep(r.currentTurn.Steps, delta)
	r.mu.Unlock()
	r.NotifyUpdate(false)
}

func (r *Recorder) OnContent(delta string) {
	r.mu.Lock()
	msg := r.getOrCreateAssistantMsg()
	msg.Content += delta
	r.mu.Unlock()
	r.NotifyUpdate(false)
}

func (r *Recorder) OnToolCall(call ollama.ToolCall) {
	r.mu.Lock()
	msg := r.getOrCreateAssistantMsg()
	tcBytes, _ := json.Marshal(call)

	found := false
	for _, existing := range msg.ToolCalls {
		var existingCall ollama.ToolCall
		if err := json.Unmarshal(existing, &existingCall); err == nil {
			if existingCall.Function.Name == call.Function.Name && string(existingCall.Function.Arguments) == string(call.Function.Arguments) {
				found = true
				break
			}
		}
	}
	if !found {
		msg.ToolCalls = append(msg.ToolCalls, tcBytes)
	}
	r.mu.Unlock()
	r.NotifyUpdate(false)
}

func (r *Recorder) OnToolStart(name string, args any) {
	r.mu.Lock()
	r.getOrCreateCurrentAssistantMsg()
	if name == "present_plan" {
		summary, steps := decodePlanStepArgs(args)
		planStep := Step{
			Type:      "plan",
			Name:      name,
			Content:   summary,
			PlanSteps: steps,
			Status:    "running",
		}
		updated := false
		for i := len(r.currentTurn.Steps) - 1; i >= 0; i-- {
			if r.currentTurn.Steps[i].Type == "plan" {
				r.currentTurn.Steps[i] = planStep
				updated = true
				break
			}
		}
		if !updated {
			r.currentTurn.Steps = append(r.currentTurn.Steps, planStep)
		}
		r.mu.Unlock()
		r.NotifyUpdate(false)
		return
	}
	r.currentTurn.Steps = append(r.currentTurn.Steps, Step{Type: "tool_exec", Name: name, Arguments: args, Status: "running"})
	r.mu.Unlock()
	r.NotifyUpdate(false)
}

func (r *Recorder) OnToolResult(name string, result string) {
	r.mu.Lock()
	if name == "present_plan" {
		for i := len(r.currentTurn.Steps) - 1; i >= 0; i-- {
			if r.currentTurn.Steps[i].Type == "plan" && r.currentTurn.Steps[i].Status == "running" {
				r.currentTurn.Steps[i].Result = result
				r.currentTurn.Steps[i].Status = "done"
				break
			}
		}
		r.mu.Unlock()
		r.NotifyUpdate(true)
		return
	}
	for i := len(r.currentTurn.Steps) - 1; i >= 0; i-- {
		if r.currentTurn.Steps[i].Type == "tool_exec" && r.currentTurn.Steps[i].Name == name && r.currentTurn.Steps[i].Status == "running" {
			r.currentTurn.Steps[i].Result = result
			r.currentTurn.Steps[i].Status = "done"
			break
		}
	}
	r.mu.Unlock()
	r.NotifyUpdate(true)
}

func decodePlanStepArgs(args any) (string, []string) {
	var payload struct {
		Summary string   `json:"summary"`
		Steps   []string `json:"steps"`
	}
	bytes, err := json.Marshal(args)
	if err != nil {
		return "", nil
	}
	if err := json.Unmarshal(bytes, &payload); err != nil {
		return "", nil
	}
	return payload.Summary, NormalizePlanSteps(payload.Steps)
}

// UpdatePlanProgress syncs the latest plan progress into the current turn timeline.
func (r *Recorder) UpdatePlanProgress(plan SessionPlan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.currentTurn.Steps) - 1; i >= 0; i-- {
		if r.currentTurn.Steps[i].Type == "plan" {
			r.currentTurn.Steps[i].Content = plan.Summary
			r.currentTurn.Steps[i].PlanSteps = append([]string(nil), plan.Steps...)
			r.currentTurn.Steps[i].Completed = plan.Completed
			r.currentTurn.Steps[i].Status = plan.Status
			break
		}
	}
	r.NotifyUpdate(false)
}

func (r *Recorder) OnMediaPreProcessing(content string) {}

func (r *Recorder) OnDone(resp ollama.ChatResponse) {
	if resp.TotalDuration <= 0 {
		return
	}
	r.mu.Lock()
	r.currentTurn.Metrics.TotalDuration += resp.TotalDuration
	r.currentTurn.Metrics.LoadDuration += resp.LoadDuration
	r.currentTurn.Metrics.PromptEvalCount += resp.PromptEvalCount
	r.currentTurn.Metrics.PromptEvalDuration += resp.PromptEvalDuration
	r.currentTurn.Metrics.EvalCount += resp.EvalCount
	r.currentTurn.Metrics.EvalDuration += resp.EvalDuration
	r.turnEnded = true
	r.mu.Unlock()
	r.NotifyUpdate(true)
}

// AddAttachmentRef appends an attachment reference to the current assistant message.
func (r *Recorder) AddAttachmentRef(ref string, mime string) {
	r.mu.Lock()
	msg := r.getOrCreateAssistantMsg()
	msg.AttachmentRefs = append(msg.AttachmentRefs, ref)
	msg.Attachments = append(msg.Attachments, AttachmentMeta{
		Name: ref,
		Mime: mime,
		Kind: "image",
	})
	r.mu.Unlock()
	r.NotifyUpdate(false)
}

func (r *Recorder) AddOrUpdateImageStep(step Step) {
	r.mu.Lock()
	r.getOrCreateCurrentAssistantMsg()
	for i := range r.currentTurn.Steps {
		if r.currentTurn.Steps[i].Type == "image_progress" && r.currentTurn.Steps[i].GenID == step.GenID {
			if step.Content != "" {
				r.currentTurn.Steps[i].Content = step.Content
			}
			if step.ImageURL != "" {
				r.currentTurn.Steps[i].ImageURL = step.ImageURL
			}
			if step.Status != "" {
				r.currentTurn.Steps[i].Status = step.Status
			}
			if step.Width > 0 {
				r.currentTurn.Steps[i].Width = step.Width
			}
			if step.Height > 0 {
				r.currentTurn.Steps[i].Height = step.Height
			}
			if step.Status == "done" && strings.TrimSpace(step.ImageURL) != "" {
				r.pruneStaleImageProgressLocked()
			}
			r.mu.Unlock()
			r.NotifyUpdate(true)
			return
		}
	}
	r.currentTurn.Steps = append(r.currentTurn.Steps, step)
	r.pruneStaleImageProgressLocked()
	r.mu.Unlock()
	r.NotifyUpdate(true)
}

func (r *Recorder) pruneStaleImageProgressLocked() {
	hasDoneImage := false
	for _, step := range r.currentTurn.Steps {
		if step.Type == "image_progress" && strings.TrimSpace(step.ImageURL) != "" {
			hasDoneImage = true
			break
		}
	}
	if !hasDoneImage {
		return
	}
	filtered := make([]Step, 0, len(r.currentTurn.Steps))
	for _, step := range r.currentTurn.Steps {
		if step.Type == "image_progress" && strings.TrimSpace(step.ImageURL) == "" {
			continue
		}
		filtered = append(filtered, step)
	}
	r.currentTurn.Steps = filtered
}

func (r *Recorder) NotifyUpdate(force bool) {
	if r == nil || r.store == nil || r.sessionID == "" {
		return
	}

	r.mu.Lock()
	now := time.Now()
	if !force && now.Sub(r.lastNotifyTime) < 300*time.Millisecond {
		r.mu.Unlock()
		return
	}
	r.lastNotifyTime = now
	gen := r.saveGen
	messages := r.snapshotMessagesLocked()
	r.mu.Unlock()

	go r.saveSnapshot(gen, messages)
}

// UpdateHistory replaces the recorded conversation history with the optimized/summarized version.
// It prefixes the kept user message and subsequent turn activity with the summary message.
func (r *Recorder) UpdateHistory(newMessages []ollama.Message, summaryContent string, numKept int) {
	if r == nil || r.store == nil || r.sessionID == "" {
		return
	}

	r.mu.Lock()
	summaryRawMsg := RawMsg{
		Role:      "system",
		Content:   summaryContent,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	allOriginalMsgs := append([]RawMsg{}, r.baseHistory...)
	allOriginalMsgs = append(allOriginalMsgs, r.activeMessages...)

	if numKept > len(allOriginalMsgs) {
		numKept = len(allOriginalMsgs)
	}
	var keptMsgs []RawMsg
	if numKept > 0 {
		keptMsgs = allOriginalMsgs[len(allOriginalMsgs)-numKept:]
	}

	// Create new history
	newHistory := append([]RawMsg{summaryRawMsg}, keptMsgs...)

	// Split into baseHistory and activeMessages
	// The current turn starts at the last user message, which is the first message in keptMsgs.
	// Since summaryRawMsg is at index 0, the current turn messages are from index 1.
	r.baseHistory = []RawMsg{summaryRawMsg}
	r.activeMessages = make([]RawMsg, len(newHistory)-1)
	copy(r.activeMessages, newHistory[1:])

	// Increment save generation and write snapshot
	r.saveGen++
	gen := r.saveGen
	messages := r.snapshotMessagesLocked()
	r.mu.Unlock()

	go r.saveSnapshot(gen, messages)
}

func (r *Recorder) FinalizeAndSave(finalHistory []ollama.Message) ([]json.RawMessage, error) {
	if r == nil || r.store == nil || r.sessionID == "" {
		return nil, nil
	}

	r.mu.Lock()
	r.saveGen++
	messages := r.mergeFinalHistoryLocked(finalHistory)
	r.mu.Unlock()

	sess, err := r.store.Get(r.sessionID)
	if err != nil {
		return messages, err
	}
	sess.Messages = messages
	if err := r.store.Save(sess); err != nil {
		return messages, err
	}
	NotifyUpdate(r.sessionID)
	return messages, nil
}

func (r *Recorder) SnapshotAndSave() ([]json.RawMessage, error) {
	if r == nil || r.store == nil || r.sessionID == "" {
		return nil, nil
	}
	r.mu.Lock()
	r.saveGen++
	messages := r.snapshotMessagesLocked()
	r.mu.Unlock()

	sess, err := r.store.Get(r.sessionID)
	if err != nil {
		return messages, err
	}
	sess.Messages = messages
	if err := r.store.Save(sess); err != nil {
		return messages, err
	}
	NotifyUpdate(r.sessionID)
	return messages, nil
}

func (r *Recorder) saveSnapshot(gen uint64, messages []json.RawMessage) {
	r.mu.Lock()
	if gen != r.saveGen {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

	sess, err := r.store.Get(r.sessionID)
	if err != nil {
		return
	}
	sess.Messages = messages
	if err := r.store.Save(sess); err == nil {
		NotifyUpdate(r.sessionID)
	}
}

func (r *Recorder) snapshotMessagesLocked() []json.RawMessage {
	snapMessages := make([]RawMsg, len(r.activeMessages))
	copy(snapMessages, r.activeMessages)

	assistantIdx := 0
	for i := range snapMessages {
		if snapMessages[i].Role != "assistant" {
			continue
		}
		var turn TurnSnapshot
		if assistantIdx < len(r.turns) {
			turn = r.turns[assistantIdx]
		} else {
			turn = r.currentTurn
		}
		snapMessages[i].Steps = FinalizeSteps(ResolveImageProgressSteps(turn.Steps, snapMessages[i].Attachments, r.sessionID))
		if turn.Metrics.TotalDuration > 0 {
			metrics := turn.Metrics
			snapMessages[i].Metrics = &metrics
		}
		snapMessages[i].Model = r.model
		snapMessages[i].Channel = r.channel
		assistantIdx++
	}

	allMessages := make([]json.RawMessage, 0, len(r.baseHistory)+len(snapMessages))
	for _, msg := range r.baseHistory {
		raw, _ := json.Marshal(msg)
		allMessages = append(allMessages, raw)
	}
	for _, msg := range snapMessages {
		raw, _ := json.Marshal(msg)
		allMessages = append(allMessages, raw)
	}
	return allMessages
}

func (r *Recorder) mergeFinalHistoryLocked(finalHistory []ollama.Message) []json.RawMessage {
	if len(finalHistory) == 0 {
		return r.snapshotMessagesLocked()
	}

	var userAssistantTimestamps []string
	var historyUserMsgs []RawMsg
	var historyAssistantMsgs []RawMsg
	for _, hm := range r.baseHistory {
		if hm.Role == "user" || hm.Role == "assistant" {
			userAssistantTimestamps = append(userAssistantTimestamps, hm.Timestamp)
		}
		if hm.Role == "user" {
			historyUserMsgs = append(historyUserMsgs, hm)
		}
		if hm.Role == "assistant" {
			historyAssistantMsgs = append(historyAssistantMsgs, hm)
		}
	}

	userMsgIdx := 0
	assistantMsgIdx := 0
	uaIdx := 0
	out := make([]json.RawMessage, 0, len(finalHistory))
	for _, msg := range finalHistory {
		if msg.Role == "system" && isInjectedContextMessage(msg.Content) {
			continue
		}
		msgTimestamp := ""
		if msg.Role == "user" || msg.Role == "assistant" {
			if uaIdx < len(userAssistantTimestamps) {
				msgTimestamp = userAssistantTimestamps[uaIdx]
				uaIdx++
			}
			if msgTimestamp == "" {
				msgTimestamp = time.Now().Format(time.RFC3339)
			}
		}

		if msg.Role == "user" {
			var orig RawMsg
			if userMsgIdx < len(historyUserMsgs) {
				orig = historyUserMsgs[userMsgIdx]
				userMsgIdx++
			}
			contentToSave := msg.Content
			if orig.Content != "" {
				contentToSave = orig.Content
			}
			rm := RawMsg{
				Role:        msg.Role,
				Content:     contentToSave,
				Name:        msg.Name,
				Images:      orig.Images,
				ImageKinds:  orig.ImageKinds,
				Attachments: orig.Attachments,
				Timestamp:   msgTimestamp,
			}
			if len(rm.Images) == 0 && len(msg.Images) > 0 {
				rm.Images = msg.Images
			}
			raw, _ := json.Marshal(rm)
			out = append(out, raw)
			continue
		}

		if msg.Role == "assistant" {
			if assistantMsgIdx < len(historyAssistantMsgs) {
				orig := historyAssistantMsgs[assistantMsgIdx]
				assistantMsgIdx++
				var tcRaw []json.RawMessage
				for _, tc := range msg.ToolCalls {
					tcBytes, _ := json.Marshal(tc)
					tcRaw = append(tcRaw, tcBytes)
				}
				rm := RawMsg{
					Role:           msg.Role,
					Content:        msg.Content,
					Name:           msg.Name,
					Images:         msg.Images,
					ToolCalls:      tcRaw,
					Timestamp:      orig.Timestamp,
					Model:          orig.Model,
					Channel:        orig.Channel,
					AttachmentRefs: orig.AttachmentRefs,
					ImageKinds:     orig.ImageKinds,
					Attachments:    orig.Attachments,
					Steps:          FinalizeSteps(ResolveImageProgressSteps(orig.Steps, orig.Attachments, r.sessionID)),
					Metrics:        orig.Metrics,
				}
				raw, _ := json.Marshal(rm)
				out = append(out, raw)
				continue
			}
		}

		var tcRaw []json.RawMessage
		for _, tc := range msg.ToolCalls {
			tcBytes, _ := json.Marshal(tc)
			tcRaw = append(tcRaw, tcBytes)
		}
		rm := RawMsg{
			Role:      msg.Role,
			Content:   msg.Content,
			Name:      msg.Name,
			Images:    msg.Images,
			ToolCalls: tcRaw,
			Timestamp: msgTimestamp,
		}
		raw, _ := json.Marshal(rm)
		out = append(out, raw)
	}

	baseAssistantCount := 0
	for _, msg := range r.baseHistory {
		if msg.Role == "assistant" {
			baseAssistantCount++
		}
	}

	turnIdx := 0
	allTurns := append([]TurnSnapshot{}, r.turns...)
	allTurns = append(allTurns, r.currentTurn)
	for i := range out {
		var rm RawMsg
		if err := json.Unmarshal(out[i], &rm); err != nil || rm.Role != "assistant" {
			continue
		}
		baseAssistantCount--
		if baseAssistantCount >= 0 {
			continue
		}
		if turnIdx < len(allTurns) {
			turn := allTurns[turnIdx]
			var thinking string
			assistantIdx := assistantIndexForNewTurn(finalHistory, turnIdx, len(r.baseHistory))
			if assistantIdx >= 0 && assistantIdx < len(finalHistory) {
				thinking = finalHistory[assistantIdx].Thinking
			}
			rm.Steps = FinalizeStepsWithThinking(ResolveImageProgressSteps(turn.Steps, rm.Attachments, r.sessionID), thinking)
			if turn.Metrics.TotalDuration > 0 {
				metrics := turn.Metrics
				rm.Metrics = &metrics
			}
			turnIdx++
		}
		rm.Thinking = ""
		rm.Model = r.model
		rm.Channel = r.channel
		if updated, err := json.Marshal(rm); err == nil {
			out[i] = updated
		}
	}

	return out
}

func assistantIndexForNewTurn(history []ollama.Message, newAssistantIdx int, baseLen int) int {
	count := -1
	for i := baseLen; i < len(history); i++ {
		if history[i].Role != "assistant" {
			continue
		}
		count++
		if count == newAssistantIdx {
			return i
		}
	}
	return -1
}
