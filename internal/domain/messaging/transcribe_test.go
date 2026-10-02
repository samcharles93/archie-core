package messaging

import "testing"

// TestTranscribedMessageTextReplacesTheMediaNote pins the boundary handoff:
// the frontend renders a voice note as its bracketed note (plus any caption),
// and the model-owning side only has that text when it transcribes. The note
// must be replaced by the transcript; the caption must survive.
func TestTranscribedMessageTextReplacesTheMediaNote(t *testing.T) {
	cases := []struct {
		name        string
		messageText string
		transcript  string
		want        string
	}{
		{
			name:        "note alone is replaced",
			messageText: "[voice message]",
			transcript:  "hello there",
			want:        "[voice transcription]\nhello there",
		},
		{
			name:        "caption after the note survives",
			messageText: "[voice message]\nignore the background noise",
			transcript:  "what time is it",
			want:        "[voice transcription]\nwhat time is it\nignore the background noise",
		},
		{
			name:        "text with no note is kept as the caption",
			messageText: "typed it by hand",
			transcript:  "spoken words",
			want:        "[voice transcription]\nspoken words\ntyped it by hand",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranscribedMessageText(tc.messageText, tc.transcript); got != tc.want {
				t.Errorf("TranscribedMessageText(%q, %q) = %q, want %q", tc.messageText, tc.transcript, got, tc.want)
			}
		})
	}
}

// TestTranscribedTextMarksProvenanceAndKeepsCaption pins the stored shape of
// a transcribed voice note: history is text-only and the original bytes are
// gone, so the marker is the only thing that tells a later reader the words
// came from a machine transcript, and the sender's caption must survive it.
func TestTranscribedTextMarksProvenanceAndKeepsCaption(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		caption    string
		want       string
	}{
		{
			name:       "transcript alone carries the marker",
			transcript: "hello there",
			want:       "[voice transcription]\nhello there",
		},
		{
			name:       "surrounding whitespace is trimmed",
			transcript: "  spaced  ",
			want:       "[voice transcription]\nspaced",
		},
		{
			name:       "caption follows the transcript",
			transcript: "what time is it",
			caption:    "ignore the background noise",
			want:       "[voice transcription]\nwhat time is it\nignore the background noise",
		},
		{
			name:    "caption without a transcript keeps its own line",
			caption: "sent by mistake",
			want:    "[voice transcription]\nsent by mistake",
		},
		{
			name:       "empty transcript still marks provenance",
			transcript: "   ",
			want:       "[voice transcription]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranscribedText(tc.transcript, tc.caption); got != tc.want {
				t.Errorf("TranscribedText(%q, %q) = %q, want %q", tc.transcript, tc.caption, got, tc.want)
			}
		})
	}
}
