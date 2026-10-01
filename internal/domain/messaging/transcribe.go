package messaging

import (
	"context"
	"errors"
	"strings"
)

// ErrTranscriptionUnavailable reports that the transcription capability
// cannot serve right now: no role configured, an unknown or unsupported
// provider, or a credential that could not be resolved. Infrastructure
// implementations wrap it so a caller can degrade -- keep the media note --
// instead of failing the turn.
var ErrTranscriptionUnavailable = errors.New("transcription capability unavailable")

// Transcriber turns recorded audio into text. It is a channel-neutral,
// call-scoped capability: the messaging media layer defines what a
// transcription is, and each frontend supplies the bytes its platform
// produced. Implementations live in internal/infrastructure/transcription;
// the domain never imports one.
//
// The same shape as image.Provider and embedding.Client: no Lifecycle,
// because "can the capability serve right now" is exactly what a returned
// error already communicates, and a missing capability must degrade rather
// than fail the turn it arrived on.
type Transcriber interface {
	// Transcribe returns the transcript of audio, which must be non-empty.
	// A failure is an error, never a panic; any error means the caller keeps
	// the media note rather than failing the turn.
	Transcribe(ctx context.Context, audio []byte) (string, error)
}

// TranscriptionProvenance prefixes text a Transcriber produced. Stored
// history is text-only, so without this marker a later reader (the model in
// a following turn, the dashboard transcript) cannot tell a machine
// transcript from something the sender typed.
const TranscriptionProvenance = "[voice transcription]"

// TranscribedText renders a transcript as the text an inbound voice note
// contributes to the turn, keeping any caption the sender attached after the
// transcript. An empty transcript still yields the provenance marker, so the
// agent knows audio arrived rather than silently treating the message as
// empty.
func TranscribedText(transcript, caption string) string {
	parts := []string{TranscriptionProvenance}
	if t := strings.TrimSpace(transcript); t != "" {
		parts = append(parts, t)
	}
	if c := strings.TrimSpace(caption); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, "\n")
}
