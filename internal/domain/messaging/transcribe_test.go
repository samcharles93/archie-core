package messaging

import "testing"

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
