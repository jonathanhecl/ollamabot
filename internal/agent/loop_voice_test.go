package agent

import "testing"

func TestVoiceLooksLikeContinuation(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "spanish ack before research", text: "Ya investigo y te aviso.", want: true},
		{name: "spanish searching variant", text: "Estoy revisando las noticias ahora mismo.", want: true},
		{name: "spanish give me a moment", text: "Dame unos minutos que lo verifico.", want: true},
		{name: "english let me check", text: "Let me check the latest news.", want: true},
		{name: "english working on it", text: "I'm searching for the updates.", want: true},
		{name: "final greeting does not continue", text: "¡Hola! Todo listo por acá.", want: false},
		{name: "final confirmation does not continue", text: "Listo, quedé con María a las tres.", want: false},
		{name: "final short answer does not continue", text: "Seis por veintisiete es ciento sesenta y dos.", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := voiceLooksLikeContinuation(tt.text); got != tt.want {
				t.Fatalf("voiceLooksLikeContinuation(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
