package agent

import "github.com/jonathanhecl/ollamabot/internal/cache"

func ShouldThink(model string, userPref bool, probePath string) bool {
	if !userPref {
		return false
	}
	return cache.SupportsCapability(probePath, model, "thinking")
}
