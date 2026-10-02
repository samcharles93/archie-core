package archied

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/transcription"
	"github.com/samcharles93/archie-core/internal/secret"
)

// TestSetupTranscriberBuildsOnTheModelOwningSide pins where the voice
// capability is constructed: beside the chat runtime, from the process's own
// [models].transcription and [providers.*]. The Messaging Service carries the
// audio bytes across the inbound wire but no longer builds this client.
func TestSetupTranscriberBuildsOnTheModelOwningSide(t *testing.T) {
	log := slog.New(slog.DiscardHandler)

	t.Run("configured role wires a transcriber", func(t *testing.T) {
		b := &boot{secrets: secret.NewRegistry()}
		b.setupTranscriber(config.Config{
			Models: map[string]string{transcription.Role: "local/whisper-large-v3"},
			Providers: map[string]config.Provider{
				"local": {Class: "openai-compatible", BaseURL: "http://127.0.0.1:8080/v1"},
			},
		}, log)
		if b.transcriber == nil {
			t.Fatal("transcriber = nil, want a client built from [models].transcription")
		}
	})

	t.Run("absent role degrades to nil", func(t *testing.T) {
		b := &boot{secrets: secret.NewRegistry()}
		b.setupTranscriber(config.Config{}, log)
		if b.transcriber != nil {
			t.Fatalf("transcriber = %v, want nil when no role is configured", b.transcriber)
		}
	})
}
