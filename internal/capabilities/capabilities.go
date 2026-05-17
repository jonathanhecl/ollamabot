package capabilities

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

type Status string

const (
	Confirmed Status = "comprobado"
	Inferred  Status = "inferido"
	Pending   Status = "pendiente"
)

var Known = []string{"completion", "tools", "thinking", "vision", "embedding", "audio", "video"}

type ModelReport struct {
	Name             string
	Family           string
	Parameters       string
	Quantization     string
	ContextLength    int64
	Capabilities     map[string]Status
	Reported         []string
	HasAudioEncoder  bool
	HasVisionEncoder bool
	ModifiedAt       string
}

func FromOllama(tag ollama.ModelTag, show ollama.ShowResponse) ModelReport {
	statuses := map[string]Status{}
	for _, capability := range Known {
		statuses[capability] = Pending
	}
	for _, capability := range show.Capabilities {
		statuses[capability] = Confirmed
	}
	hasAudio := boolValue(show.ProjectorInfo["clip.has_audio_encoder"])
	hasVision := boolValue(show.ProjectorInfo["clip.has_vision_encoder"])
	if hasAudio && statuses["audio"] != Confirmed {
		statuses["audio"] = Inferred
	}
	if hasVision && statuses["vision"] != Confirmed {
		statuses["vision"] = Inferred
	}
	statuses["video"] = Pending

	reported := append([]string{}, show.Capabilities...)
	sort.Strings(reported)
	return ModelReport{
		Name:             tag.Name,
		Family:           strings.Join(tag.Details.Families, ","),
		Parameters:       tag.Details.ParameterSize,
		Quantization:     tag.Details.QuantizationLevel,
		ContextLength:    contextLength(show.ModelInfo),
		Capabilities:     statuses,
		Reported:         reported,
		HasAudioEncoder:  hasAudio,
		HasVisionEncoder: hasVision,
		ModifiedAt:       tag.ModifiedAt,
	}
}

func StatusList(statuses map[string]Status) string {
	var parts []string
	for _, capability := range Known {
		status := statuses[capability]
		if status == "" {
			status = Pending
		}
		parts = append(parts, fmt.Sprintf("%s=%s", capability, status))
	}
	return strings.Join(parts, ", ")
}

func boolValue(value any) bool {
	asBool, ok := value.(bool)
	return ok && asBool
}

func contextLength(info map[string]any) int64 {
	for key, value := range info {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int64(typed)
		case int64:
			return typed
		case int:
			return int64(typed)
		}
	}
	return 0
}
