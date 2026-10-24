package sessions

import "testing"

func TestStripInjectedTranscriptionBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "only transcription block",
			input: `[Transcription of the audio message]: "Um, cuánto es seis por veintisiete?" (non-speech sounds: Um)`,
			want:  "",
		},
		{
			name:  "keeps user notes",
			input: "[The user sent an audio message]\n\n[An image attachment was ignored: no configured model supports images]",
			want:  "[The user sent an audio message]\n\n[An image attachment was ignored: no configured model supports images]",
		},
		{
			name:  "mixed block and notes",
			input: "[Transcription of audio attachment 1 of 2]: \"hola\"\n\n[Another audio message contains no speech. Sounds: click]",
			want:  "[Another audio message contains no speech. Sounds: click]",
		},
		{
			name:  "no block present",
			input: "plain user text",
			want:  "plain user text",
		},
		{
			name:  "looks similar but without quoted value stays",
			input: "[Transcription of the audio message] was mentioned earlier",
			want:  "[Transcription of the audio message] was mentioned earlier",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripInjectedTranscriptionBlocks(tt.input); got != tt.want {
				t.Fatalf("stripInjectedTranscriptionBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}
