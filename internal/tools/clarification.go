package tools

import (
	"fmt"
	"strings"
)

func validateClarificationOptions(options []string) error {
	for i, opt := range options {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			return fmt.Errorf("option %d cannot be empty", i+1)
		}
		if looksLikeQuestionOption(opt) {
			return fmt.Errorf(
				"option %d must be an affirmative statement, not a question (%q). "+
					"Put the question only in the 'question' field. "+
					"Each option must read like a clickable choice, e.g. \"Start a complex plan\" or \"Respond with a cheerful tone\", "+
					"never \"Do you want to start a plan?\" or \"¿Quieres iniciar un plan?\"",
				i+1, opt,
			)
		}
	}
	return nil
}

func looksLikeQuestionOption(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.HasSuffix(text, "?") || strings.HasSuffix(text, "？") {
		return true
	}
	if strings.HasPrefix(text, "¿") {
		return true
	}

	lower := strings.ToLower(text)
	questionPrefixes := []string{
		"do you ",
		"would you ",
		"can you ",
		"could you ",
		"should you ",
		"should i ",
		"do i ",
		"would i ",
		"can i ",
		"could i ",
		"want to ",
		"want me to ",
		"is it ",
		"are you ",
		"are we ",
		"will you ",
		"will i ",
		"quieres ",
		"quiero que ",
		"prefieres ",
		"prefiere ",
		"puedes ",
		"puede ",
		"podrías ",
		"podria ",
		"debo ",
		"debería ",
		"deberia ",
		"te gustaría ",
		"te gustaria ",
		"gustaría que ",
		"gustaria que ",
		"hay que ",
		"cuál ",
		"cual ",
		"qué ",
		"que quieres ",
		"como quieres ",
		"cómo quieres ",
	}
	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
