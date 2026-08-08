package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/tools"
)

// seedSessionAt persists one session directly, bypassing the tracker, with an
// explicit recency time so ordering tests are deterministic. Source fields are
// fixed so GetByChannel and List behave deterministically.
func seedSessionAt(store SessionStore, id, title string, at time.Time) SessionContext {
	sc := SessionContext{
		SessionID: id,
		Source: SessionSource{
			Platform:  "test-gw",
			BotUser:   "archie",
			ChannelID: "chat-1",
		},
		Title:        title,
		CreatedAt:    at,
		LastActiveAt: at,
	}
	_ = store.Save(context.Background(), sc)
	return sc
}

// sessionTool pulls one entry out of the session tool set.
func sessionTool(t *testing.T, entries []tools.ToolEntry, name string) tools.ToolEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("SessionTools() has no %q entry", name)
	return tools.ToolEntry{}
}

func TestSessionToolListReturnsSessions(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_list")

	now := time.Now()
	seedSessionAt(store, "sess-1", "one", now.Add(-3*time.Hour))
	seedSessionAt(store, "sess-2", "two", now.Add(-2*time.Hour))
	seedSessionAt(store, "sess-3", "", now.Add(-time.Hour))

	out, err := entry.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result, ok := out.(SessionListResult)
	if !ok {
		t.Fatalf("handler returned %T, want SessionListResult", out)
	}
	if len(result.Sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(result.Sessions))
	}
	// Newest first, per the tool contract.
	if got := result.Sessions[0].ID; got != "sess-3" {
		t.Errorf("first session = %q, want the newest (sess-3)", got)
	}
	seen := make(map[string]bool, len(result.Sessions))
	for _, s := range result.Sessions {
		seen[s.ID] = true
	}
	for _, id := range []string{"sess-1", "sess-2", "sess-3"} {
		if !seen[id] {
			t.Errorf("session %q missing from list", id)
		}
	}
}

func TestSessionToolListRespectsLimit(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_list")

	now := time.Now()
	for i := range 5 {
		seedSessionAt(store, fmt.Sprintf("sess-%d", i), "", now.Add(time.Duration(i)*time.Hour))
	}

	out, err := entry.Handler(context.Background(), map[string]any{"limit": float64(2)})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result, ok := out.(SessionListResult)
	if !ok {
		t.Fatalf("handler returned %T, want SessionListResult", out)
	}
	if len(result.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(result.Sessions))
	}
	// The two newest are kept, in order.
	if got := result.Sessions[0].ID; got != "sess-4" {
		t.Errorf("first = %q, want the newest sess-4", got)
	}
	if got := result.Sessions[1].ID; got != "sess-3" {
		t.Errorf("second = %q, want sess-3", got)
	}
}

func TestSessionToolResumeSwitchesActive(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	msg := Message{ChannelID: "chat-1", ThreadID: "thread-9"}
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", msg), "session_resume")

	target := "resume-target"
	seedSessionAt(store, target, "target session", time.Now())
	// Start somewhere else so the switch is observable.
	tr.setActive("chat-1", "thread-9", "other-session")

	out, err := entry.Handler(context.Background(), map[string]any{"session_id": target})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result, ok := out.(SessionResumeResult)
	if !ok {
		t.Fatalf("handler returned %T, want SessionResumeResult", out)
	}
	if result.SessionID != target {
		t.Errorf("session_id = %q, want %q", result.SessionID, target)
	}
	// The channel+thread come from the message captured at construction.
	if got := tr.getActive("chat-1", "thread-9"); got != target {
		t.Errorf("active = %q, want %q", got, target)
	}
}

func TestSessionToolResumeRejectsUnknown(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_resume")

	_, err := entry.Handler(context.Background(), map[string]any{"session_id": "does-not-exist"})
	if err == nil {
		t.Fatal("handler error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "no session matching") {
		t.Errorf("error = %q, want a 'no session matching' refusal", err)
	}
}

func TestSessionToolTitleSetsTitle(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_title")

	target := "title-target"
	seedSessionAt(store, target, "old title", time.Now())

	if _, err := entry.Handler(context.Background(), map[string]any{
		"session_id": target,
		"title":      "new title",
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	sc, err := store.Get(context.Background(), target)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sc == nil {
		t.Fatal("session not found")
	}
	if sc.Title != "new title" {
		t.Errorf("stored title = %q, want %q", sc.Title, "new title")
	}
}

func TestSessionToolTitleReportsPrevious(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_title")

	target := "title-prev"
	seedSessionAt(store, target, "before", time.Now())

	out, err := entry.Handler(context.Background(), map[string]any{
		"session_id": target,
		"title":      "after",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result, ok := out.(SessionTitleResult)
	if !ok {
		t.Fatalf("handler returned %T, want SessionTitleResult", out)
	}
	if result.PreviousTitle != "before" {
		t.Errorf("previous_title = %q, want %q", result.PreviousTitle, "before")
	}
	if result.Title != "after" {
		t.Errorf("title = %q, want %q", result.Title, "after")
	}
}

func TestSessionToolDeleteRemovesSession(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_delete")

	target := "delete-target"
	seedSessionAt(store, target, "doomed", time.Now())

	out, err := entry.Handler(context.Background(), map[string]any{"session_id": target})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result, ok := out.(SessionDeleteResult)
	if !ok {
		t.Fatalf("handler returned %T, want SessionDeleteResult", out)
	}
	if result.SessionID != target {
		t.Errorf("session_id = %q, want %q", result.SessionID, target)
	}

	sc, err := store.Get(context.Background(), target)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sc != nil {
		t.Error("session still present after delete")
	}
}

func TestSessionToolDeleteClearsTracker(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entry := sessionTool(t, SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"}), "session_delete")

	target := "delete-active"
	seedSessionAt(store, target, "active", time.Now())
	// Active under a flat key and a thread key; forget must sweep both.
	tr.setActive("chat-1", "", target)
	tr.setActive("chat-1", "thread-2", target)

	if _, err := entry.Handler(context.Background(), map[string]any{"session_id": target}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := tr.getActive("chat-1", ""); got != "" {
		t.Errorf("flat active = %q, want empty", got)
	}
	if got := tr.getActive("chat-1", "thread-2"); got != "" {
		t.Errorf("thread active = %q, want empty", got)
	}
}

func TestSessionToolClassifications(t *testing.T) {
	store := newFakeSessionStore()
	tr := newSessionTracker(store)
	entries := SessionTools(store, tr, "test-gw", Message{ChannelID: "chat-1"})
	if len(entries) != 4 {
		t.Fatalf("got %d tools, want 4", len(entries))
	}

	tests := []struct {
		name       string
		idempotent bool
		mutating   bool
		approval   bool
	}{
		{name: "session_list", idempotent: true},
		{name: "session_resume", mutating: true},
		{name: "session_title", mutating: true},
		{name: "session_delete", mutating: true, approval: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := sessionTool(t, entries, tc.name)
			if e.Toolset != "session" {
				t.Errorf("toolset = %q, want %q", e.Toolset, "session")
			}
			if got := e.Classification.IsIdempotent(); got != tc.idempotent {
				t.Errorf("idempotent = %v, want %v", got, tc.idempotent)
			}
			if got := e.Classification.IsMutating(); got != tc.mutating {
				t.Errorf("mutating = %v, want %v", got, tc.mutating)
			}
			if got := e.Classification.IsApprovalRequired(); got != tc.approval {
				t.Errorf("requires_approval = %v, want %v", got, tc.approval)
			}
		})
	}
}

func TestSessionToolNilStoreOmitsTools(t *testing.T) {
	if entries := SessionTools(nil, nil, "test-gw", Message{ChannelID: "chat-1"}); len(entries) != 0 {
		t.Errorf("SessionTools(nil, ...) = %d entries, want 0", len(entries))
	}
}

// TestSessionToolNilTrackerOmitsTrackerTools checks that a tracker-dependent
// tool is not advertised when no tracker is wired, matching the TaskTools rule
// that a nil backend omits its tool rather than registering one that always
// fails.
func TestSessionToolNilTrackerOmitsTrackerTools(t *testing.T) {
	store := newFakeSessionStore()
	entries := SessionTools(store, nil, "test-gw", Message{ChannelID: "chat-1"})
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d tools (%v), want the 2 store-only tools", len(entries), names)
	}
	for _, name := range names {
		switch name {
		case "session_resume", "session_delete":
			t.Errorf("%q needs the tracker and must not be advertised without one", name)
		}
	}
}
