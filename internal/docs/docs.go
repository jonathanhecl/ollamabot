package docs

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jonathanhecl/ollamabot/internal/capabilities"
)

type ReferenceData struct {
	BaseURL       string
	OllamaVersion string
	GeneratedAt   time.Time
}

func Reference(data ReferenceData) string {
	if data.GeneratedAt.IsZero() {
		data.GeneratedAt = time.Now()
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Ollama API Reference\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", data.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Base URL: `%s`\n\n", data.BaseURL)
	if data.OllamaVersion != "" {
		fmt.Fprintf(&b, "Ollama version: `%s`\n\n", data.OllamaVersion)
	}
	fmt.Fprintf(&b, "## Sources\n\n")
	for _, source := range sources {
		fmt.Fprintf(&b, "- [%s](%s)\n", source.Title, source.URL)
	}
	fmt.Fprintf(&b, "\n## Confirmed REST Patterns\n\n")
	fmt.Fprintf(&b, "- Text chat: `POST /api/chat` with `messages` and `stream:false`.\n")
	fmt.Fprintf(&b, "- Vision: `POST /api/chat` with `messages[].images` containing raw base64 image data.\n")
	fmt.Fprintf(&b, "- Tool calling: send `tools`, read `message.tool_calls`, execute locally, append `role:\"tool\"` with `tool_name`, then call chat again.\n")
	fmt.Fprintf(&b, "- Structured output: send `format:\"json\"` or a JSON Schema object in `format`, then validate the returned content.\n")
	fmt.Fprintf(&b, "- Thinking: send `think:true` or a model-specific level when required; read `message.thinking` separately from `message.content`.\n")
	fmt.Fprintf(&b, "- Embeddings: `POST /api/embed` with text input; response contains `embeddings`.\n\n")
	fmt.Fprintf(&b, "- Running models and memory: `GET /api/ps` returns loaded models with `size_vram`, `expires_at`, and active `context_length`.\n\n")
	fmt.Fprintf(&b, "## Minimal Payloads\n\n")
	fmt.Fprintf(&b, "### Text\n\n```json\n%s\n```\n\n", textPayload)
	fmt.Fprintf(&b, "### Image\n\n`images` values must be raw base64, not a `data:image/...` URI.\n\n```json\n%s\n```\n\n", imagePayload)
	fmt.Fprintf(&b, "### Tool Calling\n\n```json\n%s\n```\n\n", toolPayload)
	fmt.Fprintf(&b, "### Structured JSON\n\n```json\n%s\n```\n\n", jsonPayload)
	fmt.Fprintf(&b, "### Thinking\n\n```json\n%s\n```\n\n", thinkingPayload)
	fmt.Fprintf(&b, "### Embeddings\n\n```json\n%s\n```\n\n", embeddingPayload)
	fmt.Fprintf(&b, "## Pending Confirmation\n\n")
	fmt.Fprintf(&b, "- Audio: models that report `audio` in `/api/show.capabilities` are marked as metadata-confirmed. End-to-end audio still requires `probe audio --audio PATH`; local `gemma4:e2b` WAV testing currently fails with an Ollama runner 500.\n")
	fmt.Fprintf(&b, "- Video: no native Ollama REST video input is treated as confirmed; future support should start as frame extraction into vision models.\n")
	return b.String()
}

func Inventory(reports []capabilities.ModelReport, data ReferenceData) string {
	if data.GeneratedAt.IsZero() {
		data.GeneratedAt = time.Now()
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].Name < reports[j].Name
	})

	var b bytes.Buffer
	fmt.Fprintf(&b, "# Local Model Inventory\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", data.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Base URL: `%s`\n\n", data.BaseURL)
	if data.OllamaVersion != "" {
		fmt.Fprintf(&b, "Ollama version: `%s`\n\n", data.OllamaVersion)
	}
	fmt.Fprintf(&b, "| Model | Family | Params | Quant | Context | Capabilities | Encoders |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | ---: | --- | --- |\n")
	for _, report := range reports {
		encoders := []string{}
		if report.HasVisionEncoder {
			encoders = append(encoders, "vision")
		}
		if report.HasAudioEncoder {
			encoders = append(encoders, "audio")
		}
		if len(encoders) == 0 {
			encoders = append(encoders, "-")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %d | %s | %s |\n",
			escape(report.Name),
			escape(report.Family),
			escape(report.Parameters),
			escape(report.Quantization),
			report.ContextLength,
			escape(capabilities.StatusList(report.Capabilities)),
			escape(strings.Join(encoders, ",")),
		)
	}
	fmt.Fprintf(&b, "\n## Status Semantics\n\n")
	fmt.Fprintf(&b, "- `comprobado`: reported by Ollama `/api/show.capabilities` or validated by a probe.\n")
	fmt.Fprintf(&b, "- `inferido`: inferred from model/projector metadata, not yet validated end-to-end.\n")
	fmt.Fprintf(&b, "- `pendiente`: not reported and not locally verified.\n")
	return b.String()
}

func escape(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}

type source struct {
	Title string
	URL   string
}

var sources = []source{
	{"Ollama API introduction", "https://docs.ollama.com/api/introduction"},
	{"Chat API", "https://docs.ollama.com/api/chat"},
	{"List models", "https://docs.ollama.com/api/tags"},
	{"List running models", "https://docs.ollama.com/api/ps"},
	{"Vision", "https://docs.ollama.com/capabilities/vision"},
	{"Tool calling", "https://docs.ollama.com/capabilities/tool-calling"},
	{"Structured outputs", "https://docs.ollama.com/capabilities/structured-outputs"},
	{"Thinking", "https://docs.ollama.com/capabilities/thinking"},
	{"Embeddings", "https://docs.ollama.com/capabilities/embeddings"},
	{"OpenAI compatibility", "https://docs.ollama.com/api/openai-compatibility"},
	{"Gemma4 model page", "https://ollama.com/library/gemma4%3Alatest"},
	{"Audio input feature request", "https://github.com/ollama/ollama/issues/11798"},
}

const textPayload = `{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "Say ok"}],
  "stream": false
}`

const imagePayload = `{
  "model": "qwen3-vl:4b",
  "messages": [{
    "role": "user",
    "content": "Describe this image.",
    "images": ["<raw-base64-image>"]
  }],
  "stream": false
}`

const toolPayload = `{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "What is the temperature in Tokyo?"}],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_temperature",
      "description": "Get the current temperature for a city",
      "parameters": {
        "type": "object",
        "required": ["city"],
        "properties": {"city": {"type": "string"}}
      }
    }
  }],
  "stream": false
}`

const jsonPayload = `{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "Return a JSON object named ollamabot."}],
  "format": {
    "type": "object",
    "properties": {"name": {"type": "string"}, "ok": {"type": "boolean"}},
    "required": ["name", "ok"]
  },
  "stream": false
}`

const thinkingPayload = `{
  "model": "qwen3:8b",
  "messages": [{"role": "user", "content": "How many r letters are in strawberry?"}],
  "think": true,
  "stream": false
}`

const embeddingPayload = `{
  "model": "nomic-embed-text:latest",
  "input": "The quick brown fox jumps over the lazy dog."
}`
