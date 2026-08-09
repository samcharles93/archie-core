package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubTitleGenerator records calls and answers from a per-session table.
// When block is non-nil, GenerateTitle blocks until the channel closes,
// which tests use to hold a proposal in flight.
type stubTitleGenerator struct {
	mu         sync.Mutex
	calls      int
	texts      []string
	sessionIDs []string
	titles     map[string]string
	err        error
	block      chan struct{}
	// panicMsg, when set, makes GenerateTitle panic after any block wait.
	panicMsg string
}

func (s *stubTitleGenerator) GenerateTitle(_ context.Context, sessionID, firstMessage string) (string, error) {
	s.mu.Lock()
	s.calls++
	s.sessionIDs = append(s.sessionIDs, sessionID)
	s.texts = append(s.texts, firstMessage)
	s.mu.Unlock()
	if s.block != nil {
		<-s.block
	}
	if s.err != nil {
		return "", s.err
	}
	if s.panicMsg != "" {
		panic(s.panicMsg)
	}
	return s.titles[sessionID], nil
}

func (s *stubTitleGenerator) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubTitleGenerator) lastText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.texts) == 0 {
		return ""
	}
	return s.texts[len(s.texts)-1]
}

// llmResponder is a deterministic LLMResponder for turn-path tests.
func llmResponder(reply string) LLMResponder {
	return func(context.Context, Message) (string, error) { return reply, nil }
}

// waitForTitle polls the store until sessionID carries the wanted title.
// Title generation is asynchronous, so tests must wait rather than
// assume the reply path finished the write.
func waitForTitle(t *testing.T, store SessionStore, sessionID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sc, err := store.Get(context.Background(), sessionID)
		if err == nil && sc != nil && sc.Title == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	sc, _ := store.Get(context.Background(), sessionID)
	var got string
	if sc != nil {
		got = sc.Title
	}
	t.Fatalf("title for %s never became %q (got %q)", sessionID, want, got)
}

func TestCleanGeneratedTitle(t *testing.T) {
	long := strings.Repeat("x", maxTitleRunes+10)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Deploy the NATS worker", "Deploy the NATS worker"},
		{"collapses whitespace", "  spaced   out\t\ntext  ", "spaced out text"},
		{"strips wrapping double quotes", `"Deploy the worker"`, "Deploy the worker"},
		{"strips wrapping single quotes", "'Deploy the worker'", "Deploy the worker"},
		{"strips trailing period", "Deploy the worker.", "Deploy the worker"},
		{"empty", "", ""},
		{"blank", "   \n\t ", ""},
		{"quote junk only", `""`, ""},
		{"truncates long titles", long, strings.Repeat("x", maxTitleRunes) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanGeneratedTitle(tc.in); got != tc.want {
				t.Errorf("cleanGeneratedTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-2 * time.Hour), "2h ago"},
		{"days", now.Add(-3 * 24 * time.Hour), "3d ago"},
		{"older dates", now.Add(-10 * 24 * time.Hour), "Jul 18"},
		{"zero time", time.Time{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeAge(tc.at, now); got != tc.want {
				t.Errorf("relativeAge(%v) = %q, want %q", tc.at, got, tc.want)
			}
		})
	}
}

func TestRenderSessionList(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	sessions := []SessionContext{
		{
			SessionID: "aaaaaaaa-1111-2222-3333-444444444444", Title: "Deploy NATS worker image",
			CreatedAt: now.Add(-5 * time.Minute), LastActiveAt: now.Add(-5 * time.Minute),
		},
		{
			SessionID: "bbbbbbbb-1111-2222-3333-444444444444", Title: "Fix telegram redelivery",
			CreatedAt: now.Add(-2 * time.Hour), LastActiveAt: now.Add(-2 * time.Hour),
		},
		{
			SessionID: "cccccccc-1111-2222-3333-444444444444",
			CreatedAt: now.Add(-3 * 24 * time.Hour), LastActiveAt: now.Add(-3 * 24 * time.Hour),
		},
	}
	got := renderSessionList(sessions, "aaaaaaaa-1111-2222-3333-444444444444", now)
	want := "Sessions (3):\n" +
		"  1. aaaaaaaa  Deploy NATS worker image  · active · 5m ago\n" +
		"  2. bbbbbbbb  Fix telegram redelivery   · 2h ago\n" +
		"  3. cccccccc  (untitled)                · 3d ago"
	if got != want {
		t.Errorf("renderSessionList:\n got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderSessionListEmpty(t *testing.T) {
	if got := renderSessionList(nil, "", time.Now()); got != "No sessions." {
		t.Errorf("renderSessionList(nil) = %q, want %q", got, "No sessions.")
	}
}

func TestRouteSessionsRenderedList(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	store := NewSessionStoreMemory()
	ctx := context.Background()
	now := time.Now()
	for _, sc := range []SessionContext{
		{
			SessionID: "aaaaaaaa-1111-2222-3333-444444444444", Title: "Deploy NATS worker image",
			CreatedAt: now.Add(-5 * time.Minute), LastActiveAt: now.Add(-5 * time.Minute),
		},
		{
			SessionID: "bbbbbbbb-1111-2222-3333-444444444444",
			CreatedAt: now.Add(-2 * time.Hour), LastActiveAt: now.Add(-2 * time.Hour),
		},
	} {
		if err := store.Save(ctx, sc); err != nil {
			t.Fatalf("save session: %v", err)
		}
	}
	r.InitSessions(store)
	reply, err := r.Route(ctx, Message{Text: "/sessions"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	for _, want := range []string{
		"Sessions (2):",
		"1. aaaaaaaa  Deploy NATS worker image",
		"2. bbbbbbbb  (untitled)",
		"5m ago",
		"2h ago",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want substring %q", reply, want)
		}
	}
}

func TestRouteAutoTitlesUntitledSession(t *testing.T) {
	r := NewRouter(nil, llmResponder("reply ok"), "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{titles: map[string]string{}}
	r.Titles = gen
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "Deploy the NATS worker"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gen.titles[sessionID] = "Deploy NATS worker"
	if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "Deploy the NATS worker"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	waitForTitle(t, store, sessionID, "Deploy NATS worker")
	if n := gen.callCount(); n != 1 {
		t.Errorf("generator calls = %d, want 1", n)
	}
	if got := gen.lastText(); got != "Deploy the NATS worker" {
		t.Errorf("title source = %q, want the triggering message", got)
	}
}

// waitForTitleIdle waits until no title proposal is in flight. A
// correctly-skipped session never spawns a proposal, so it returns
// immediately; a wrongly-spawned one stays in flight (blocked on the
// stub) and times out. This pins that the background decision ran and
// finished, instead of passing vacuously because nothing ever started.
func waitForTitleIdle(t *testing.T, r *Router) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.titlingMu.Lock()
		idle := len(r.titling) == 0
		r.titlingMu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("title proposal still in flight after timeout")
}

func TestRouteDoesNotRetitleTitledSession(t *testing.T) {
	r := NewRouter(nil, llmResponder("reply ok"), "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{titles: map[string]string{}}
	// Never closed: if a proposal were wrongly spawned against this
	// titled session, it would block here and waitForTitleIdle would time
	// out -- turning the vacuous "no call" assertion into a hard failure.
	gen.block = make(chan struct{})
	r.Titles = gen
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "hello"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	sc, err := store.Get(ctx, sessionID)
	if err != nil || sc == nil {
		t.Fatalf("get session: %v", err)
	}
	sc.Title = "Manual title"
	if err := store.Save(ctx, *sc); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "more text"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	waitForTitleIdle(t, r)
	if n := gen.callCount(); n != 0 {
		t.Errorf("generator calls = %d, want 0 for a titled session", n)
	}
	sc, _ = store.Get(ctx, sessionID)
	if sc == nil || sc.Title != "Manual title" {
		t.Errorf("title = %q, want it untouched", sc.Title)
	}
}

func TestRouteAutoTitleErrorKeepsUntitled(t *testing.T) {
	r := NewRouter(nil, llmResponder("reply ok"), "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{err: errors.New("model down"), titles: map[string]string{}}
	r.Titles = gen
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	// The proposal is spawned (the session is untitled) and must drain
	// after the generator errors, leaving the session untitled.
	waitForTitleIdle(t, r)
	sc, _ := store.Get(ctx, sessionID)
	if sc == nil || sc.Title != "" {
		t.Errorf("title = %q, want empty after generator error", sc.Title)
	}
}

func TestRouteAutoTitleInFlightGuard(t *testing.T) {
	r := NewRouter(nil, llmResponder("reply ok"), "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{titles: map[string]string{}}
	gen.block = make(chan struct{})
	r.Titles = gen
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gen.titles[sessionID] = "Deploy worker"
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy"}); err != nil {
			t.Errorf("Route 1: %v", err)
		}
	}()
	// Wait until the first proposal is in flight and blocked.
	deadline := time.Now().Add(2 * time.Second)
	for gen.callCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	<-done
	// A second turn while the first proposal is still blocked must not
	// spawn a second call: the guard lets one proposal win the session.
	if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy again"}); err != nil {
		t.Fatalf("Route 2: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := gen.callCount(); n != 1 {
		t.Errorf("generator calls = %d, want 1 (in-flight guard)", n)
	}
	close(gen.block)
	waitForTitle(t, store, sessionID, "Deploy worker")
}

func TestRouteStreamAutoTitlesUntitledSession(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{titles: map[string]string{}}
	r.Titles = gen
	r.LLMStream = func(_ context.Context, _ Message, onDelta func(string)) (string, error) {
		onDelta("r")
		return "reply ok", nil
	}
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "stream me"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gen.titles[sessionID] = "Stream topic"
	if _, err := r.RouteStream(ctx, Message{ChannelID: "ch", From: "u", Text: "stream me"}, func(string) {}); err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	waitForTitle(t, store, sessionID, "Stream topic")
}

func TestRouteAutoTitlePanicIsContained(t *testing.T) {
	r := NewRouter(nil, llmResponder("reply ok"), "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{titles: map[string]string{}, panicMsg: "boom"}
	r.Titles = gen
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	// If the recover is ever removed, the panic in the background
	// goroutine crashes the test process: this test fails loudly.
	waitForTitleIdle(t, r)
	sc, _ := store.Get(ctx, sessionID)
	if sc == nil || sc.Title != "" {
		t.Errorf("title = %q, want empty after panicking generator", sc.Title)
	}
}

func TestRetryAutoTitlesUntitledSession(t *testing.T) {
	r := NewRouter(nil, llmResponder("reply ok"), "test-gw")
	store := newFakeSessionStore()
	r.InitSessions(store)
	gen := &stubTitleGenerator{titles: map[string]string{}}
	r.Titles = gen
	ctx := context.Background()
	sessionID, err := r.ResolveSessionKey(ctx, Message{ChannelID: "ch", From: "u", Text: "deploy the worker"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gen.titles[sessionID] = "Deploy worker"
	if err := store.SaveMessage(ctx, sessionID, Message{From: "u", Text: "deploy the worker"}); err != nil {
		t.Fatalf("save user message: %v", err)
	}
	if err := store.SaveMessage(ctx, sessionID, Message{From: "bot", Text: "sure"}); err != nil {
		t.Fatalf("save reply: %v", err)
	}
	if _, err := r.Route(ctx, Message{ChannelID: "ch", From: "u", Text: "/retry"}); err != nil {
		t.Fatalf("Route /retry: %v", err)
	}
	waitForTitle(t, store, sessionID, "Deploy worker")
	if got := gen.lastText(); got != "deploy the worker" {
		t.Errorf("title source = %q, want the replayed message", got)
	}
}
