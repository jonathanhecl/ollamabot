package config

import "strings"

const (
	ModelRoleMain       = "main"
	ModelRoleSubagent   = "subagent"
	ModelRoleVision     = "vision"
	ModelRoleAudio      = "audio"
	ModelRoleEmbed      = "embed"
	ModelRoleEmbeddings = "embeddings"
	ModelRoleLearning   = "learning"
	ModelRoleImage      = "image"
)

// ResolveModel returns the model assigned to a role by the application config.
// The main model is the single source of truth for chat execution; optional
// roles fall back to main only where that behavior is part of the product
// contract.
func ResolveModel(cfg Config, role string) string {
	main := strings.TrimSpace(cfg.OllamaDefaultModel)

	switch strings.ToLower(strings.TrimSpace(role)) {
	case ModelRoleMain, "":
		return main
	case ModelRoleSubagent:
		if model := strings.TrimSpace(cfg.OllamaModelSubagent); model != "" {
			return model
		}
		return main
	case ModelRoleVision:
		return strings.TrimSpace(cfg.OllamaModelVision)
	case ModelRoleAudio:
		return strings.TrimSpace(cfg.OllamaModelAudio)
	case ModelRoleEmbed, ModelRoleEmbeddings:
		return strings.TrimSpace(cfg.OllamaModelEmbed)
	case ModelRoleLearning:
		if model := strings.TrimSpace(cfg.OllamaModelLearning); model != "" {
			return model
		}
		return main
	case ModelRoleImage:
		return strings.TrimSpace(cfg.OllamaModelImage)
	default:
		return ""
	}
}
