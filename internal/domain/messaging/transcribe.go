package messaging

import (
	"context"
	"errors"
	"strings"
)

// ErrTranscriptionUnavailable reports that transcription is not configured
// or cannot run.
var ErrTranscriptionUnavailable = errors.New("transcription capability unavailable")

// Transcriber turns recorded audio into text.
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

// TranscribedText returns the transcript with a provenance marker, followed
// by any caption.
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

// TranscribedMessageText replaces the media note in messageText with the
// transcript, keeping any caption after it.
func TranscribedMessageText(messageText, transcript string) string {
	note, caption, _ := strings.Cut(messageText, "\n")
	if !isMediaNote(note) {
		return TranscribedText(transcript, messageText)
	}
	return TranscribedText(transcript, caption)
}

// isMediaNote reports whether s is a channel frontend's bracketed media
// placeholder such as "[voice message]".
func isMediaNote(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']'
}
