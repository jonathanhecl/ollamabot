package agent

import "testing"

func TestRedirectSearchToFetch(t *testing.T) {
	tests := []struct {
		name     string
		goal     string
		query    string
		wantURL  string
		wantOK   bool
	}{
		{
			name:    "query repeats the exact URL",
			goal:    "Instala este skill https://github.com/foo/bar/blob/main/SKILL.md por favor",
			query:   "https://github.com/foo/bar/blob/main/SKILL.md",
			wantURL: "https://github.com/foo/bar/blob/main/SKILL.md",
			wantOK:  true,
		},
		{
			name:    "query contains the URL with extra words",
			goal:    "lee https://github.com/foo/bar/blob/main/SKILL.md",
			query:   "instrucciones skill https://github.com/foo/bar/blob/main/SKILL.md",
			wantURL: "https://github.com/foo/bar/blob/main/SKILL.md",
			wantOK:  true,
		},
		{
			name:    "query is the URL without scheme",
			goal:    "usa este link https://github.com/foo/bar/blob/main/SKILL.md",
			query:   "github.com/foo/bar/blob/main/SKILL.md",
			wantURL: "https://github.com/foo/bar/blob/main/SKILL.md",
			wantOK:  true,
		},
		{
			name:    "unrelated query does not redirect",
			goal:    "lee https://github.com/foo/bar/blob/main/SKILL.md y dime el clima",
			query:   "clima en buenos aires",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "no URL in goal",
			goal:    "busca documentación de ollama",
			query:   "ollama api docs",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "empty query",
			goal:    "lee https://example.com/a",
			query:   "",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "multiple URLs picks the matching one",
			goal:    "compara https://example.com/uno con https://example.com/dos",
			query:   "https://example.com/dos",
			wantURL: "https://example.com/dos",
			wantOK:  true,
		},
		{
			name:    "URL with trailing punctuation in goal",
			goal:    "mira https://example.com/page.",
			query:   "https://example.com/page",
			wantURL: "https://example.com/page",
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotOK := redirectSearchToFetch(tt.goal, tt.query)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotURL != tt.wantURL {
				t.Fatalf("url = %q, want %q", gotURL, tt.wantURL)
			}
		})
	}
}
