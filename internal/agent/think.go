package agent

import "github.com/jonathanhecl/ollamabot/internal/cache"

func ShouldThink(model string, enabled bool, probePath string) bool {
	if !enabled {
		return false
	}
	return cache.SupportsCapability(probePath, model, "thinking")
}
