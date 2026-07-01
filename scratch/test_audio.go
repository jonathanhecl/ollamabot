package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"

	"github.com/jonathanhecl/ollamabot/internal/ollama"
)

func main() {
	client := ollama.NewClient("http://localhost:11434")
	payload, err := os.ReadFile("test.wav")
	if err != nil {
		log.Fatalf("failed to read test.wav: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(payload)

	prompt := "Analyze this audio. YOUR ABSOLUTE HIGHEST PRIORITY IS TO TRANSCRIBE ANY SPEECH VERBATIM. Start your response with the verbatim transcription of the speech. If the audio contains spoken words, transcribe them completely and accurately first. Do not summarize, paraphrase, or omit any spoken words. Only after the transcription, or if there is absolutely no speech, describe other sounds heard, speaker's tone, background noise, or other contextually relevant audio events."

	log.Printf("Calling chat with new prompt...")
	resp, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model: "gemma4:e2b",
		Messages: []ollama.Message{
			{Role: "user", Content: prompt, Images: []string{encoded}},
		},
		Options: map[string]any{"temperature": 0, "num_ctx": 8000},
	})
	if err != nil {
		log.Fatalf("chat failed: %v", err)
	}
	fmt.Printf("\n--- RESPONSE ---\n%s\n----------------\n", resp.Message.Content)
}
