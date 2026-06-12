package ollama

import "encoding/json"

type VersionResponse struct {
	Version string `json:"version"`
}

type TagsResponse struct {
	Models []ModelTag `json:"models"`
}

type PsResponse struct {
	Models []RunningModel `json:"models"`
}

type ModelTag struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt string       `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
}

type RunningModel struct {
	Name          string       `json:"name"`
	Model         string       `json:"model"`
	Size          int64        `json:"size"`
	Digest        string       `json:"digest"`
	Details       ModelDetails `json:"details"`
	ExpiresAt     string       `json:"expires_at"`
	SizeVRAM      int64        `json:"size_vram"`
	ContextLength int64        `json:"context_length"`
}

type ModelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type ShowResponse struct {
	License       string         `json:"license"`
	Modelfile     string         `json:"modelfile"`
	Parameters    string         `json:"parameters"`
	Template      string         `json:"template"`
	Details       ModelDetails   `json:"details"`
	ModelInfo     map[string]any `json:"model_info"`
	ProjectorInfo map[string]any `json:"projector_info"`
	Capabilities  []string       `json:"capabilities"`
	ModifiedAt    string         `json:"modified_at"`
}

type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []Tool         `json:"tools,omitempty"`
	Format   any            `json:"format,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
	Stream   *bool          `json:"stream,omitempty"`
	Think    any            `json:"think,omitempty"`
}

type ChatResponse struct {
	Model              string  `json:"model"`
	Message            Message `json:"message"`
	Done               bool    `json:"done"`
	DoneReason         string  `json:"done_reason"`
	TotalDuration      int64   `json:"total_duration,omitempty"`
	LoadDuration       int64   `json:"load_duration,omitempty"`
	PromptEvalCount    int     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64   `json:"prompt_eval_duration,omitempty"`
	EvalCount          int     `json:"eval_count,omitempty"`
	EvalDuration       int64   `json:"eval_duration,omitempty"`
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Thinking  string     `json:"thinking,omitempty"`
	Images    []string   `json:"images,omitempty"`
	Name      string     `json:"name,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Type     string       `json:"type,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Index     int             `json:"index,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type Tool struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type EmbedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type EmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

// GenerateRequest is used for image generation models (diffusion models like Flux)
type GenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Images  []string       `json:"images,omitempty"` // For img2img
	Stream  *bool          `json:"stream,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// GenerateResponse is the response from image generation models
type GenerateResponse struct {
	Model           string   `json:"model"`
	Response        string   `json:"response,omitempty"` // Text response if any
	Images          []string `json:"images,omitempty"`   // Generated image data (base64)
	Done            bool     `json:"done"`
	DoneReason      string   `json:"done_reason,omitempty"`
	Completed       int      `json:"completed,omitempty"` // Progress step
	Total           int      `json:"total,omitempty"`     // Total steps
	PromptEvalCount int      `json:"prompt_eval_count,omitempty"`
	EvalCount       int      `json:"eval_count,omitempty"`
}
