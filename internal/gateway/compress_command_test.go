package gateway

import (
	"context"
	"strings"
	"testing"
)

// /compress dispatch. Every case here is a command an operator can type, and
// the contract is that a command either does what it says or explains itself
// -- it never silently does something else to an irreplaceable history.
func TestCompressCommandDispatch(t *testing.T) {
	tests := []struct {
		name string
		rest string
		// wantApplied says the history must actually be rewritten.
		wantApplied bool
		// wantReply is a fragment the reply must contain.
		wantReply string
		// wantProtected, when non-zero, is the number of trailing messages
		// that must survive alongside the summary.
		wantProtected int
	}{
		{
			// The preview's own closing line says "Run /compress to apply".
			// Taking the preview branch here made that instruction a loop:
			// the documented way to apply compression could never apply it.
			name:          "bare compress applies",
			rest:          "",
			wantApplied:   true,
			wantReply:     "Compressed:",
			wantProtected: DefaultCompressionConfig().ProtectLast,
		},
		{
			name:      "--preview does not apply",
			rest:      "--preview",
			wantReply: "preview",
		},
		{
			name:      "--dry-run does not apply",
			rest:      "--dry-run",
			wantReply: "preview",
		},
		{
			name:          "here N protects N",
			rest:          "here 5",
			wantApplied:   true,
			wantReply:     "Compressed:",
			wantProtected: 5,
		},
		{
			// Previously fell through to a full default compression,
			// rewriting history from a command that named no count.
			name:      "here without a count explains itself",
			rest:      "here",
			wantReply: "Usage",
		},
		{
			name:      "here with a non-number explains itself",
			rest:      "here abc",
			wantReply: "Usage",
		},
		{
			// focus has no implementation: cfg was never modified, so this
			// ran a full default compression and reported success.
			name:      "focus reports that it is unsupported",
			rest:      "focus auth",
			wantReply: "not supported",
		},
		{
			name:      "focus without a topic explains itself",
			rest:      "focus",
			wantReply: "Usage",
		},
		{
			// Previously compressed the whole history.
			name:      "an unknown argument explains itself",
			rest:      "asdf",
			wantReply: "Usage",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewSessionStoreMemory("test-node")
			t.Cleanup(func() { _ = store.Close() })
			r, sessionID := routerOn(t, store, "chat-1")

			const seeded = 40
			seedForCompress(t, store, sessionID, seeded)

			reply, err := r.handleCompress(ctx, Message{ChannelID: "chat-1"}, tc.rest)
			if err != nil {
				t.Fatalf("handleCompress(%q): %v", tc.rest, err)
			}
			if !strings.Contains(strings.ToLower(reply), strings.ToLower(tc.wantReply)) {
				t.Errorf("reply = %q, want it to contain %q", reply, tc.wantReply)
			}

			count, err := store.MessageCount(ctx, sessionID)
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}

			if !tc.wantApplied {
				if count != seeded {
					t.Fatalf("history was rewritten by %q: count = %d, want the original %d",
						"/compress "+tc.rest, count, seeded)
				}
				return
			}

			if count == seeded {
				t.Fatalf("history was not compressed by %q: count is still %d", "/compress "+tc.rest, count)
			}
			if tc.wantProtected > 0 {
				// head + summary + tail
				want := DefaultCompressionConfig().ProtectFirst + 1 + tc.wantProtected
				if count != want {
					t.Errorf("count = %d, want %d (first %d + summary + last %d)",
						count, want, DefaultCompressionConfig().ProtectFirst, tc.wantProtected)
				}
			}
		})
	}
}

// The preview reports a message count from the store but token figures from a
// fixed window, so on a long history the two halves of one sentence described
// different conversations -- and it advised against a compression that was in
// fact needed.
func TestCompressPreviewReadsTheWholeHistory(t *testing.T) {
	ctx := context.Background()
	store := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = store.Close() })
	r, sessionID := routerOn(t, store, "chat-1")

	// Enough messages to exceed the old fixed 200-message read, with the
	// bulk of the tokens outside that window.
	seedForCompress(t, store, sessionID, 40)
	for i := range 250 {
		if err := store.SaveMessage(ctx, sessionID, Message{
			From: "alice", Text: "tiny", SourceID: pad(i), At: at(dur(1000 + i)),
		}); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	preview, err := r.compressPreview(ctx, sessionID)
	if err != nil {
		t.Fatalf("compressPreview: %v", err)
	}
	if strings.Contains(preview, "not needed") {
		t.Fatalf("preview says compression is not needed, but applying it compresses: %q", preview)
	}

	applied, err := r.applyCompress(ctx, sessionID, DefaultCompressionConfig())
	if err != nil {
		t.Fatalf("applyCompress: %v", err)
	}
	if !strings.Contains(applied, "Compressed:") {
		t.Fatalf("apply did not compress after the preview said it would: %q", applied)
	}
}

func pad(i int) string {
	return "late-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
}
