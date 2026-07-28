package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSessionStore implements SessionStore for tests. Messages are
// stored in memory with causal ordering preserved.
type fakeSessionStore struct {
	mu       sync.Mutex
	sessions map[string]SessionContext
	messages map[string][]Message // sessionID → messages
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		sessions: make(map[string]SessionContext),
		messages: make(map[string][]Message),
	}
}

func (f *fakeSessionStore) Save(_ context.Context, s SessionContext) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.SessionID] = s
	return nil
}

func (f *fakeSessionStore) Get(_ context.Context, sessionID string) (*SessionContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (f *fakeSessionStore) GetByChannel(_ context.Context, platform, channelID string) ([]SessionContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []SessionContext
	for _, s := range f.sessions {
		if s.Source.Platform == platform && s.Source.ChannelID == channelID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSessionStore) Delete(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, sessionID)
	delete(f.messages, sessionID)
	return nil
}

func (f *fakeSessionStore) Touch(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[sessionID]
	if !ok {
		return nil
	}
	s.LastActiveAt = time.Now()
	f.sessions[sessionID] = s
	return nil
}

func (f *fakeSessionStore) List(_ context.Context) ([]SessionContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []SessionContext
	for _, s := range f.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeSessionStore) Close() error { return nil }

func (f *fakeSessionStore) SaveMessage(_ context.Context, sessionID string, msg Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[sessionID] = append(f.messages[sessionID], msg)
	return nil
}

func (f *fakeSessionStore) RecentMessages(_ context.Context, sessionID string, n int) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.messages[sessionID]
	if len(msgs) == 0 {
		return nil, nil
	}
	start := max(len(msgs)-n, 0)
	out := make([]Message, len(msgs)-start)
	copy(out, msgs[start:])
	return out, nil
}

func (f *fakeSessionStore) DeleteRecentMessages(_ context.Context, sessionID string, n int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.messages[sessionID]
	if len(msgs) == 0 {
		return 0, nil
	}
	toDelete := min(n, len(msgs))
	f.messages[sessionID] = msgs[:len(msgs)-toDelete]
	return toDelete, nil
}

func (f *fakeSessionStore) MessageCount(_ context.Context, sessionID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages[sessionID]), nil
}

func (f *fakeSessionStore) SaveMessages(_ context.Context, sessionID string, msgs []Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing := f.messages[sessionID]
	out := make([]Message, len(existing)+len(msgs))
	copy(out, existing)
	copy(out[len(existing):], msgs)
	f.messages[sessionID] = out
	return nil
}

func (f *fakeSessionStore) SearchMessages(_ context.Context, sessionID, query string, limit int) ([]Message, error) {
	return nil, nil
}

// newTestRouter returns a Router with a session store wired for testing.
func newTestRouter(gatewayName string) *Router {
	store := newFakeSessionStore()
	r := NewRouter(nil, nil, gatewayName)
	r.InitSessions(store)
	r.Identity = "archie"
	return r
}

// ── /start ─────────────────────────────────────────────────────────────────

func TestRouteStart(t *testing.T) {
	r := newTestRouter("telegram")
	reply, err := r.Route(context.Background(), Message{Text: "/start"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Archie is running") {
		t.Errorf("reply = %q, want acknowledgement", reply)
	}
}

// ── /new and /reset ────────────────────────────────────────────────────────

func TestRouteNewCreatesSession(t *testing.T) {
	r := newTestRouter("telegram")
	msg := Message{ChannelID: "chat-1", ThreadID: "", From: "alice"}

	reply, err := r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-1"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "New session created") {
		t.Errorf("reply = %q, want session creation confirmation", reply)
	}

	// Verify session was created.
	sessionID := r.sessionTracker.getActive("chat-1", "")
	if sessionID == "" {
		t.Fatal("expected active session")
	}
	sc, err := r.sessionTracker.sessions.Get(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if sc == nil {
		t.Fatal("session not found")
	}
	_ = msg // used implicitly through channel ID
}

func TestRouteNewWithTitle(t *testing.T) {
	r := newTestRouter("telegram")

	reply, err := r.Route(context.Background(), Message{Text: "/new debugging session", ChannelID: "chat-2"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "debugging session") {
		t.Errorf("reply = %q, want title in confirmation", reply)
	}

	sessionID := r.sessionTracker.getActive("chat-2", "")
	sc, err := r.sessionTracker.sessions.Get(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if sc.Title != "debugging session" {
		t.Errorf("title = %q, want %q", sc.Title, "debugging session")
	}
}

func TestRouteResetAlias(t *testing.T) {
	r := newTestRouter("telegram")

	// /new creates initial session.
	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-3"})
	oldSession := r.sessionTracker.getActive("chat-3", "")
	if oldSession == "" {
		t.Fatal("expected initial session")
	}

	// /reset creates a new session.
	reply, err := r.Route(context.Background(), Message{Text: "/reset", ChannelID: "chat-3"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "New session created") {
		t.Errorf("reply = %q, want session creation", reply)
	}

	newSession := r.sessionTracker.getActive("chat-3", "")
	if newSession == oldSession {
		t.Error("expected new session ID after /reset")
	}
}

func TestRouteNewNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	reply, err := r.Route(context.Background(), Message{Text: "/new"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

// ── /topic ─────────────────────────────────────────────────────────────────

func TestRouteTopicHelp(t *testing.T) {
	r := newTestRouter("telegram")
	reply, err := r.Route(context.Background(), Message{Text: "/topic help", ChannelID: "chat-4"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage: /topic") {
		t.Errorf("reply = %q, want usage info", reply)
	}
}

func TestRouteTopicListSessions(t *testing.T) {
	r := newTestRouter("telegram")

	// Create an initial session via /new.
	_, _ = r.Route(context.Background(), Message{Text: "/new alpha", ChannelID: "chat-5"})
	_, _ = r.Route(context.Background(), Message{Text: "/new beta", ChannelID: "chat-5", ThreadID: "1"})

	reply, err := r.Route(context.Background(), Message{Text: "/topic", ChannelID: "chat-5"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "alpha") {
		t.Errorf("reply = %q, want alpha session listed", reply)
	}
	if !strings.Contains(reply, "beta") {
		t.Errorf("reply = %q, want beta session listed", reply)
	}
}

func TestRouteTopicSwitch(t *testing.T) {
	r := newTestRouter("telegram")

	// Create two sessions.
	_, _ = r.Route(context.Background(), Message{Text: "/new first", ChannelID: "chat-6"})
	firstID := r.sessionTracker.getActive("chat-6", "")

	_, _ = r.Route(context.Background(), Message{Text: "/new second", ChannelID: "chat-6"})
	secondID := r.sessionTracker.getActive("chat-6", "")

	// Switch back to first using prefix.
	prefix := firstID[:min(8, len(firstID))]
	reply, err := r.Route(context.Background(), Message{Text: "/topic " + prefix, ChannelID: "chat-6"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Switched to session") {
		t.Errorf("reply = %q, want switch confirmation", reply)
	}

	current := r.sessionTracker.getActive("chat-6", "")
	if current != firstID {
		t.Errorf("active = %s, want %s (firstID=%s, secondID=%s)", current[:8], firstID[:8], firstID[:8], secondID[:8])
	}
}

func TestRouteTopicUnknown(t *testing.T) {
	r := newTestRouter("telegram")
	reply, err := r.Route(context.Background(), Message{Text: "/topic nonexist", ChannelID: "chat-7"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No session found") {
		t.Errorf("reply = %q, want 'No session found'", reply)
	}
}

// ── /retry ─────────────────────────────────────────────────────────────────

func TestRouteRetryNoSession(t *testing.T) {
	r := newTestRouter("telegram")
	reply, err := r.Route(context.Background(), Message{Text: "/retry", ChannelID: "chat-8"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No active session") {
		t.Errorf("reply = %q, want 'No active session'", reply)
	}
}

func TestRouteRetryRemovesLastMessage(t *testing.T) {
	r := newTestRouter("telegram")

	// Set up a session with messages.
	sessionID := "test-retry-session"
	sc := SessionContext{
		SessionID: sessionID,
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-9"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	_ = r.sessionTracker.sessions.Save(context.Background(), sc)
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "alice", Text: "hello"})
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "archie", Text: "hi there"})
	r.sessionTracker.setActive("chat-9", "", sessionID)

	// Verify initial count.
	count, _ := r.sessionTracker.sessions.MessageCount(context.Background(), sessionID)
	if count != 2 {
		t.Fatalf("initial count = %d, want 2", count)
	}

	// Retry.
	reply, err := r.Route(context.Background(), Message{Text: "/retry", ChannelID: "chat-9"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}

	// When LLM is nil, retry reports LLM not configured but message was removed.
	if !strings.Contains(reply, "LLM") && !strings.Contains(reply, "configured") {
		t.Errorf("reply = %q, want LLM not configured message", reply)
	}

	count, _ = r.sessionTracker.sessions.MessageCount(context.Background(), sessionID)
	if count != 1 {
		t.Errorf("count after retry = %d, want 1", count)
	}
}

// ── /undo ──────────────────────────────────────────────────────────────────

func TestRouteUndoRemovesOne(t *testing.T) {
	r := newTestRouter("telegram")

	sessionID := "test-undo"
	sc := SessionContext{
		SessionID: sessionID,
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-10"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	_ = r.sessionTracker.sessions.Save(context.Background(), sc)
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "alice", Text: "msg1"})
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "archie", Text: "reply1"})
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "alice", Text: "msg2"})
	r.sessionTracker.setActive("chat-10", "", sessionID)

	reply, err := r.Route(context.Background(), Message{Text: "/undo", ChannelID: "chat-10"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Removed 1 message") {
		t.Errorf("reply = %q, want removal confirmation", reply)
	}

	count, _ := r.sessionTracker.sessions.MessageCount(context.Background(), sessionID)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestRouteUndoRemovesMultiple(t *testing.T) {
	r := newTestRouter("telegram")

	sessionID := "test-undo-multi"
	sc := SessionContext{
		SessionID: sessionID,
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-11"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	_ = r.sessionTracker.sessions.Save(context.Background(), sc)
	for i := range 5 {
		_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "alice", Text: fmt.Sprintf("msg%d", i)})
	}
	r.sessionTracker.setActive("chat-11", "", sessionID)

	reply, err := r.Route(context.Background(), Message{Text: "/undo 3", ChannelID: "chat-11"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Removed 3 messages") {
		t.Errorf("reply = %q, want 'Removed 3 messages'", reply)
	}

	count, _ := r.sessionTracker.sessions.MessageCount(context.Background(), sessionID)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestRouteUndoNoMessages(t *testing.T) {
	r := newTestRouter("telegram")

	sessionID := "test-undo-empty"
	sc := SessionContext{
		SessionID: sessionID,
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-12"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	_ = r.sessionTracker.sessions.Save(context.Background(), sc)
	r.sessionTracker.setActive("chat-12", "", sessionID)

	reply, err := r.Route(context.Background(), Message{Text: "/undo", ChannelID: "chat-12"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Nothing to undo") {
		t.Errorf("reply = %q, want 'Nothing to undo'", reply)
	}
}

func TestRouteUndoInvalidN(t *testing.T) {
	r := newTestRouter("telegram")

	sessionID := "test-undo-invalid"
	sc := SessionContext{
		SessionID: sessionID,
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-13"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	_ = r.sessionTracker.sessions.Save(context.Background(), sc)
	r.sessionTracker.setActive("chat-13", "", sessionID)

	reply, err := r.Route(context.Background(), Message{Text: "/undo notanumber", ChannelID: "chat-13"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage: /undo") {
		t.Errorf("reply = %q, want usage message", reply)
	}
}

// ── /title ─────────────────────────────────────────────────────────────────

func TestRouteTitleSet(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-14"})
	sessionID := r.sessionTracker.getActive("chat-14", "")

	reply, err := r.Route(context.Background(), Message{Text: "/title debugging the crash", ChannelID: "chat-14"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Session title set to: debugging the crash") {
		t.Errorf("reply = %q, want title confirmation", reply)
	}

	sc, _ := r.sessionTracker.sessions.Get(context.Background(), sessionID)
	if sc.Title != "debugging the crash" {
		t.Errorf("title = %q, want %q", sc.Title, "debugging the crash")
	}
}

func TestRouteTitleShow(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new my topic", ChannelID: "chat-15"})

	reply, err := r.Route(context.Background(), Message{Text: "/title", ChannelID: "chat-15"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "my topic") {
		t.Errorf("reply = %q, want current title", reply)
	}
}

func TestRouteTitleNoTitle(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-16"})

	reply, err := r.Route(context.Background(), Message{Text: "/title", ChannelID: "chat-16"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No title set") {
		t.Errorf("reply = %q, want 'No title set'", reply)
	}
}

// ── /branch and /fork ──────────────────────────────────────────────────────

func TestRouteBranchCreatesChildSession(t *testing.T) {
	r := newTestRouter("telegram")

	// Create parent with history.
	_, _ = r.Route(context.Background(), Message{Text: "/new parent session", ChannelID: "chat-17"})
	parentID := r.sessionTracker.getActive("chat-17", "")

	// Add messages to parent.
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), parentID, Message{From: "alice", Text: "hello"})
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), parentID, Message{From: "archie", Text: "hi"})

	// Branch.
	reply, err := r.Route(context.Background(), Message{Text: "/branch experiment", ChannelID: "chat-17"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "experiment") {
		t.Errorf("reply = %q, want branch name in confirmation", reply)
	}
	if !strings.Contains(reply, "inherited messages") {
		t.Errorf("reply = %q, want inherited message count", reply)
	}

	childID := r.sessionTracker.getActive("chat-17", "")
	if childID == parentID {
		t.Error("expected new child session to be active")
	}

	childSC, _ := r.sessionTracker.sessions.Get(context.Background(), childID)
	if childSC.ParentSessionID != parentID {
		t.Errorf("parent = %s, want %s", childSC.ParentSessionID, parentID)
	}
	if childSC.BranchName != "experiment" {
		t.Errorf("branch name = %q, want 'experiment'", childSC.BranchName)
	}

	// Child should have inherited messages.
	count, _ := r.sessionTracker.sessions.MessageCount(context.Background(), childID)
	if count != 2 {
		t.Errorf("inherited count = %d, want 2", count)
	}
}

func TestRouteForkAlias(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new parent", ChannelID: "chat-18"})
	parentID := r.sessionTracker.getActive("chat-18", "")
	_ = r.sessionTracker.sessions.SaveMessage(context.Background(), parentID, Message{From: "alice", Text: "msg"})

	reply, err := r.Route(context.Background(), Message{Text: "/fork", ChannelID: "chat-18"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Branched session") {
		t.Errorf("reply = %q, want branch confirmation from /fork", reply)
	}
}

// ── /compress and /compact ─────────────────────────────────────────────────

func TestRouteCompressPreview(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-19"})
	sessionID := r.sessionTracker.getActive("chat-19", "")

	// Add a bunch of messages (not enough to trigger compression by default).
	for i := range 10 {
		_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "alice", Text: fmt.Sprintf("message number %d", i)})
	}

	reply, err := r.Route(context.Background(), Message{Text: "/compress --preview", ChannelID: "chat-19"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Compression") {
		t.Errorf("reply = %q, want compression info", reply)
	}
}

func TestRouteCompactAlias(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-20"})
	sessionID := r.sessionTracker.getActive("chat-20", "")

	for i := range 5 {
		_ = r.sessionTracker.sessions.SaveMessage(context.Background(), sessionID, Message{From: "alice", Text: fmt.Sprintf("msg %d with some content to make it longer than a few chars", i)})
	}

	reply, err := r.Route(context.Background(), Message{Text: "/compact --dry-run", ChannelID: "chat-20"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Compression") {
		t.Errorf("reply = %q, want compression info via /compact", reply)
	}
}

func TestRouteCompressNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	reply, err := r.Route(context.Background(), Message{Text: "/compress"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

// ── sessionTracker ─────────────────────────────────────────────────────────

func TestSessionTrackerResolveCreatesAndReuses(t *testing.T) {
	st := newFakeSessionStore()
	tr := newSessionTracker(st)
	ctx := context.Background()

	// First resolve creates a session.
	id1, err := tr.resolve(ctx, "telegram", "archie", "chat-X", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id1 == "" {
		t.Fatal("expected session ID")
	}

	// Second resolve reuses the same session.
	id2, err := tr.resolve(ctx, "telegram", "archie", "chat-X", "")
	if err != nil {
		t.Fatalf("resolve 2: %v", err)
	}
	if id2 != id1 {
		t.Errorf("expected same session ID, got %s != %s", id2, id1)
	}

	// Different thread gets a different session.
	id3, err := tr.resolve(ctx, "telegram", "archie", "chat-X", "5")
	if err != nil {
		t.Fatalf("resolve thread: %v", err)
	}
	if id3 == id1 {
		t.Error("different thread should get a new session")
	}
}

// ── aliases ────────────────────────────────────────────────────────────────

func TestRouteResetAndNewAreSame(t *testing.T) {
	r := newTestRouter("telegram")

	r1, _ := r.Route(context.Background(), Message{Text: "/new test", ChannelID: "chat-a"})
	r2, _ := r.Route(context.Background(), Message{Text: "/reset test2", ChannelID: "chat-a"})

	// Both should create a new session clearing previous.
	if !strings.Contains(r1, "test") {
		t.Errorf("/new reply missing title: %q", r1)
	}
	if !strings.Contains(r2, "test2") {
		t.Errorf("/reset reply missing title: %q", r2)
	}
}

func TestRouteBranchAndForkAreSame(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-b"})

	r1, _ := r.Route(context.Background(), Message{Text: "/branch", ChannelID: "chat-b"})
	r2, _ := r.Route(context.Background(), Message{Text: "/fork", ChannelID: "chat-b"})

	if !strings.Contains(r1, "Branched session") {
		t.Errorf("/branch reply: %q", r1)
	}
	if !strings.Contains(r2, "Branched session") {
		t.Errorf("/fork reply: %q", r2)
	}
}

func TestRouteCompressAndCompactAreSame(t *testing.T) {
	r := newTestRouter("telegram")

	_, _ = r.Route(context.Background(), Message{Text: "/new", ChannelID: "chat-c"})

	r1, _ := r.Route(context.Background(), Message{Text: "/compress --dry-run", ChannelID: "chat-c"})
	r2, _ := r.Route(context.Background(), Message{Text: "/compact --dry-run", ChannelID: "chat-c"})

	// Both should show compression info.
	if !strings.Contains(r1, "Compression") {
		t.Errorf("/compress reply: %q", r1)
	}
	if !strings.Contains(r2, "Compression") {
		t.Errorf("/compact reply: %q", r2)
	}
}

// ── LocalCommands includes all session commands ────────────────────────────

func TestLocalCommandsIncludeSessionCommands(t *testing.T) {
	cmds := LocalCommands()
	cmdSet := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		cmdSet[c] = true
	}

	required := []string{"/new", "/reset", "/topic", "/retry", "/undo", "/title", "/branch", "/fork", "/compress", "/compact"}
	for _, c := range required {
		if !cmdSet[c] {
			t.Errorf("LocalCommands missing %q", c)
		}
	}
}
