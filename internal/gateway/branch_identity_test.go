package gateway

import (
	"context"
	"strings"
	"testing"
)

// A branch copies the parent's history into a new session. Those copies used
// to keep the parent's canonical MessageIDs, because saveMessageLocked
// honours an incoming ID and handleBranch fed it messages read straight back
// from the parent. newMessageID's own doc claims the opposite -- "the same
// upstream message copied into a branch gets its own identity" -- and the
// architecture requires exact branch-point correlation on stable MessageIDs,
// which is ambiguous when three sessions hold the same ID.
//
// It also defeats dedup in the child: redelivering an upstream message that
// was inherited appends, because the inherited copy carries an ID derived
// from the parent's session rather than the child's.
func TestBranchGivesInheritedMessagesTheirOwnIdentity(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)

			r := NewRouter(nil, nil, "telegram")
			r.InitSessions(store)
			r.Identity = "archie"

			parentID, err := r.sessionTracker.resolve(ctx, "telegram", "archie", "chat-1", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			for i, text := range []string{"one", "two", "three"} {
				if err := store.SaveMessage(ctx, parentID, Message{
					From: "alice", Text: text, SourceID: "tg-" + text, At: at(dur(i)),
				}); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}
			parentMsgs, err := store.RecentMessages(ctx, parentID, 100)
			if err != nil {
				t.Fatalf("RecentMessages(parent): %v", err)
			}

			reply, err := r.Route(ctx, Message{Text: "/branch side-quest", ChannelID: "chat-1"})
			if err != nil {
				t.Fatalf("Route(/branch): %v", err)
			}
			if !strings.Contains(reply, "Branched") {
				t.Fatalf("reply = %q, want a branch confirmation", reply)
			}
			childID := r.sessionTracker.getActive("chat-1", "")
			if childID == parentID {
				t.Fatal("branch did not switch the active session")
			}

			childMsgs, err := store.RecentMessages(ctx, childID, 100)
			if err != nil {
				t.Fatalf("RecentMessages(child): %v", err)
			}
			if len(childMsgs) != len(parentMsgs) {
				t.Fatalf("child has %d messages, want the parent's %d", len(childMsgs), len(parentMsgs))
			}

			parentIDs := make(map[string]struct{}, len(parentMsgs))
			for _, m := range parentMsgs {
				parentIDs[m.MessageID] = struct{}{}
			}
			for _, m := range childMsgs {
				if _, shared := parentIDs[m.MessageID]; shared {
					t.Errorf("inherited %q kept the parent's MessageID %q: two sessions "+
						"now claim one canonical identity", m.Text, m.MessageID)
				}
				if m.SourceID == "" {
					t.Errorf("inherited %q lost its SourceID; upstream correlation is gone", m.Text)
				}
				if want := CanonicalMessageID(childID, m.SourceID); m.MessageID != want {
					t.Errorf("inherited %q: MessageID = %q, want the child-scoped %q",
						m.Text, m.MessageID, want)
				}
			}

			// Dedup must work in the child on its own terms.
			if err := store.SaveMessage(ctx, childID, Message{
				From: "alice", Text: "two", SourceID: "tg-two", At: at(dur(1)),
			}); err != nil {
				t.Fatalf("redeliver into child: %v", err)
			}
			after, err := store.MessageCount(ctx, childID)
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}
			if after != len(childMsgs) {
				t.Errorf("redelivery into the branch appended: count = %d, want %d",
					after, len(childMsgs))
			}
		})
	}
}
