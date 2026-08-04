package gateway

import (
	"context"
	"testing"
	"time"
)

// resolve claims to find "the most recent session matching this thread and
// bot" but took whichever session the store happened to return first. The
// tracker cache is in-memory, so every daemon restart re-resolves from the
// store -- and dropped the operator back into an arbitrary older session,
// answering against pre-/new history. LastActiveAt was populated on every
// record and never consulted.
func TestResolvePicksTheMostRecentSession(t *testing.T) {
	// Session IDs are chosen so that ordering by ID disagrees with ordering
	// by recency: a store that returns document order hands back "aaa" or
	// "zzz" first depending on direction, and neither is the answer.
	tests := []struct {
		name     string
		sessions []struct {
			id     string
			active time.Duration
		}
		want string
	}{
		{
			name: "newest sorts last by id",
			sessions: []struct {
				id     string
				active time.Duration
			}{
				{"aaa-old", 0},
				{"zzz-new", time.Hour},
			},
			want: "zzz-new",
		},
		{
			name: "newest sorts first by id",
			sessions: []struct {
				id     string
				active time.Duration
			}{
				{"zzz-old", 0},
				{"aaa-new", time.Hour},
			},
			want: "aaa-new",
		},
		{
			name: "newest is in the middle",
			sessions: []struct {
				id     string
				active time.Duration
			}{
				{"aaa", 0},
				{"mmm", 2 * time.Hour},
				{"zzz", time.Hour},
			},
			want: "mmm",
		},
	}

	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					ctx := context.Background()
					store := newStore(t)

					for _, s := range tc.sessions {
						if err := store.Save(ctx, SessionContext{
							SessionID: s.id,
							Source: SessionSource{
								Platform:  "telegram",
								BotUser:   "archie",
								ChannelID: "chat-1",
							},
							CreatedAt:    base.Add(s.active),
							LastActiveAt: base.Add(s.active),
						}); err != nil {
							t.Fatalf("Save %s: %v", s.id, err)
						}
					}

					// A cold tracker, as after a restart.
					tr := newSessionTracker(store)
					got, err := tr.resolve(ctx, "telegram", "archie", "chat-1", "")
					if err != nil {
						t.Fatalf("resolve: %v", err)
					}
					if got != tc.want {
						t.Errorf("resolve = %q, want the most recently active %q", got, tc.want)
					}
				})
			}
		})
	}
}

// Matching is still scoped to the bot and thread: a more recent session
// belonging to another identity or another thread must not be picked up.
func TestResolveIgnoresOtherBotsAndThreads(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)

			save := func(id, botUser, threadID string, offset time.Duration) {
				t.Helper()
				if err := store.Save(ctx, SessionContext{
					SessionID: id,
					Source: SessionSource{
						Platform:  "telegram",
						BotUser:   botUser,
						ChannelID: "chat-1",
						ThreadID:  threadID,
					},
					CreatedAt:    base.Add(offset),
					LastActiveAt: base.Add(offset),
				}); err != nil {
					t.Fatalf("Save %s: %v", id, err)
				}
			}

			save("ours", "archie", "", 0)
			save("other-bot", "winter", "", 5*time.Hour)
			save("other-thread", "archie", "t-9", 9*time.Hour)

			tr := newSessionTracker(store)
			got, err := tr.resolve(ctx, "telegram", "archie", "chat-1", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != "ours" {
				t.Errorf("resolve = %q, want %q: a newer session for another bot or "+
					"thread must not be adopted", got, "ours")
			}
		})
	}
}

// List promises newest-first in its interface doc. It reversed document
// order, which is by session ID, so the dashboard and /sessions listed an
// arbitrary order that looked like recency.
func TestListIsNewestFirst(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)

			// Creation order deliberately disagrees with ID order.
			order := []struct {
				id     string
				offset time.Duration
			}{
				{"zzz-oldest", 0},
				{"aaa-middle", time.Hour},
				{"mmm-newest", 2 * time.Hour},
			}
			for _, o := range order {
				if err := store.Save(ctx, SessionContext{
					SessionID:    o.id,
					Source:       SessionSource{Platform: "telegram", ChannelID: "chat-1"},
					CreatedAt:    base.Add(o.offset),
					LastActiveAt: base.Add(o.offset),
				}); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}

			got, err := store.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			want := []string{"mmm-newest", "aaa-middle", "zzz-oldest"}
			if len(got) != len(want) {
				t.Fatalf("List returned %d sessions, want %d", len(got), len(want))
			}
			for i, w := range want {
				if got[i].SessionID != w {
					t.Errorf("List[%d] = %q, want %q (full order %v)",
						i, got[i].SessionID, w, sessionIDs(got))
				}
			}
		})
	}
}

func sessionIDs(ss []SessionContext) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.SessionID)
	}
	return out
}

// The two backends must order the same records identically. The Go side used
// to prefer LastActiveAt whenever it was set, while SQLite used
// MAX(last_active_at, created_at); those agree only when LastActiveAt is the
// later of the two. A clock step, or a record written by an older version,
// made the store's own ORDER BY and the caller's pick disagree.
func TestRecencyAgreesWhenLastActivePredatesCreation(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)

			// X was created later but carries an older LastActiveAt.
			save := func(id string, created, active time.Duration) {
				t.Helper()
				if err := store.Save(ctx, SessionContext{
					SessionID:    id,
					Source:       SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-1"},
					CreatedAt:    base.Add(created),
					LastActiveAt: base.Add(active),
				}); err != nil {
					t.Fatalf("Save %s: %v", id, err)
				}
			}
			save("x-created-late", 5*time.Hour, -5*time.Hour)
			save("y-normal", 3*time.Hour, 3*time.Hour)

			got, err := store.GetByChannel(ctx, "telegram", "chat-1")
			if err != nil {
				t.Fatalf("GetByChannel: %v", err)
			}
			if len(got) != 2 {
				t.Fatalf("got %d sessions, want 2", len(got))
			}
			if got[0].SessionID != "x-created-late" {
				t.Errorf("order = %v, want x-created-late first (its creation is "+
					"the later instant)", sessionIDs(got))
			}

			// The caller's own pick must match the store's order.
			tr := newSessionTracker(store)
			resolved, err := tr.resolve(ctx, "telegram", "archie", "chat-1", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if resolved != got[0].SessionID {
				t.Errorf("resolve = %q but the store orders %q first: the caller "+
					"and the store disagree", resolved, got[0].SessionID)
			}
		})
	}
}

// Resolving a session is what "the session is in use" means, so it must
// update LastActiveAt. Without it the recency ordering ranked by creation
// time, and an operator who switched to an older session with /topic and
// worked there was dropped back into the newer, abandoned one on restart.
func TestResolveMarksTheSessionActive(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)

			// "old" is the one the operator is actually working in.
			for _, s := range []struct {
				id     string
				offset time.Duration
			}{{"old", 0}, {"new", 5 * time.Hour}} {
				if err := store.Save(ctx, SessionContext{
					SessionID:    s.id,
					Source:       SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-1"},
					CreatedAt:    base.Add(s.offset),
					LastActiveAt: base.Add(s.offset),
				}); err != nil {
					t.Fatal(err)
				}
			}

			tr := newSessionTracker(store)
			tr.setActive("chat-1", "", "old")
			if got, err := tr.resolve(ctx, "telegram", "archie", "chat-1", ""); err != nil || got != "old" {
				t.Fatalf("resolve = (%q, %v), want old", got, err)
			}

			// A cold tracker, as after a restart, must now pick "old".
			cold := newSessionTracker(store)
			got, err := cold.resolve(ctx, "telegram", "archie", "chat-1", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != "old" {
				t.Errorf("after restart resolve = %q, want %q: working in a session "+
					"did not mark it active, so recency still ranked by creation",
					got, "old")
			}
		})
	}
}
