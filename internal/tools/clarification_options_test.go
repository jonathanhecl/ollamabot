package tools

import "testing"

func TestLooksLikeQuestionOption(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Iniciar un plan complejo", false},
		{"Respond with a cheerful tone", false},
		{"¿Quieres iniciar un plan?", true},
		{"Do you want to start a plan?", true},
		{"Prefieres que revise tus gustos", true},
		{"Revisar mi conocimiento actual sobre tus gustos", false},
		{"Start a complex plan (define steps and goals)", false},
		{"Should I review your preferences?", true},
	}

	for _, tc := range cases {
		got := looksLikeQuestionOption(tc.text)
		if got != tc.want {
			t.Fatalf("looksLikeQuestionOption(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestValidateClarificationOptionsRejectsQuestions(t *testing.T) {
	err := validateClarificationOptions([]string{
		"Iniciar un plan complejo",
		"¿Quieres que responda con tono casual?",
	})
	if err == nil {
		t.Fatal("expected validation error for question-style option")
	}
}

func TestValidateClarificationOptionsAcceptsStatements(t *testing.T) {
	if err := validateClarificationOptions([]string{
		"Iniciar un plan complejo",
		"Responder con tono casual sobre el mensaje",
		"Revisar mi conocimiento sobre tus gustos",
	}); err != nil {
		t.Fatalf("expected valid options, got error: %v", err)
	}
}
