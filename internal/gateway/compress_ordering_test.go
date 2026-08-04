package gateway

import (
	"context"
	"strings"
	"testing"
)

// The summary stands in for the messages it replaced, so it belongs where
// they were: after the protected head, before the protected tail.
//
// Preserving the retained messages' original timestamps -- which is what
// stops compression destroying their identity -- means ordering is no longer
// the order the caller wrote them in. The summary is a new record, so the
// monotonic clamp stamps it with now and it sorts after everything it was
// supposed to precede. The model then reads the whole tail of the
// conversation and is told afterwards that earlier context was summarised.
func TestCompressSummaryLandsBetweenHeadAndTail(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)
			r, sessionID := routerOn(t, store, "chat-1")

			seedForCompress(t, store, sessionID, 40)
			if _, err := r.applyCompress(ctx, sessionID, DefaultCompressionConfig()); err != nil {
				t.Fatalf("applyCompress: %v", err)
			}

			after, err := store.RecentMessages(ctx, sessionID, 1000)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}

			marker := DefaultCompressionConfig().SummaryMarker
			idx := -1
			for i, m := range after {
				if strings.Contains(m.Text, marker) {
					idx = i
					break
				}
			}
			if idx < 0 {
				t.Fatal("no summary in the compressed history")
			}

			cfg := DefaultCompressionConfig()
			if idx != cfg.ProtectFirst {
				t.Errorf("summary is at index %d of %d, want %d (after the %d "+
					"protected head messages, before the tail): the model reads "+
					"the recent conversation and is only then told that earlier "+
					"context was summarised",
					idx, len(after), cfg.ProtectFirst, cfg.ProtectFirst)
			}
			if idx == len(after)-1 {
				t.Error("the summary is the newest message in the session")
			}
		})
	}
}

// PriorReply decides "was this message already answered?" by looking at
// whether the next turn came from the bot. A compression summary is
// attributed to the bot and is not an answer to anything, so it must not be
// mistaken for one -- otherwise a redelivered question is answered with a
// compression banner and the model is never called.
func TestPriorReplyIgnoresACompressionSummary(t *testing.T) {
	const (
		sessionID = "chat-1"
		identity  = "archie"
	)
	marker := DefaultCompressionConfig().SummaryMarker

	history := []Message{
		{
			MessageID: CanonicalMessageID(sessionID, "tg-unanswered"),
			SourceID:  "tg-unanswered",
			From:      "alice",
			Text:      "what is 2+2?",
		},
		{
			MessageID: "summary-record",
			From:      identity,
			Text:      marker + "\n\n[36 messages removed (~9000 tokens)]",
		},
	}

	if got := PriorReply(history, sessionID, "tg-unanswered", identity); got != "" {
		t.Errorf("PriorReply = %q, want \"\": the question was never answered, "+
			"so redelivering it must re-run the model rather than replay a "+
			"compression banner as the answer", got)
	}
}

// A genuine reply is still recognised, so the dedup that stops a redelivered
// message being answered twice keeps working.
func TestPriorReplyStillMatchesARealReply(t *testing.T) {
	const (
		sessionID = "chat-1"
		identity  = "archie"
	)
	history := []Message{
		{
			MessageID: CanonicalMessageID(sessionID, "tg-1"),
			SourceID:  "tg-1",
			From:      "alice",
			Text:      "ping",
		},
		{MessageID: "reply", From: identity, Text: "pong"},
	}
	if got := PriorReply(history, sessionID, "tg-1", identity); got != "pong" {
		t.Errorf("PriorReply = %q, want %q", got, "pong")
	}
}
