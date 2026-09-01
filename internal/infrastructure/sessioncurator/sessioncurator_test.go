package sessioncurator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// --- fakes -------------------------------------------------------------

type fakeConversations struct {
	sessions map[string]time.Time // id -> last active
	messages map[string][]curator.ConversationMessage
}

func (f *fakeConversations) RecentSessions(_ context.Context, since time.Time) ([]curator.SessionSummary, error) {
	var out []curator.SessionSummary
	for id, at := range f.sessions {
		if at.Before(since) {
			continue
		}
		out = append(out, curator.SessionSummary{ID: id, LastActive: at})
	}
	return out, nil
}

func (f *fakeConversations) Messages(_ context.Context, sessionID string, n int) ([]curator.ConversationMessage, error) {
	msgs := f.messages[sessionID]
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs, nil
}

// fakeLLM returns a canned response per call, or the same response every
// time if only one is configured.
type fakeLLM struct {
	responses []string
	err       error
	calls     int
}

func (f *fakeLLM) Chat(context.Context, curator.ChatRequest) (curator.ChatResult, error) {
	if f.err != nil {
		return curator.ChatResult{}, f.err
	}
	i := f.calls
	f.calls++
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	return curator.ChatResult{Text: f.responses[i]}, nil
}

type fakeEngine struct {
	writes []domainmemory.Observation
}

func (f *fakeEngine) Name() string                    { return "builtin" }
func (f *fakeEngine) Version() string                 { return "test" }
func (f *fakeEngine) Manifest() domainmemory.Manifest { return domainmemory.Manifest{} }
func (f *fakeEngine) Bind(domainmemory.Registrar)     {}
func (f *fakeEngine) Start(context.Context) error     { return nil }
func (f *fakeEngine) Health(context.Context) domainmemory.Health {
	return domainmemory.Health{Status: domainmemory.HealthHealthy}
}
func (f *fakeEngine) Stop(context.Context) error { return nil }

func (f *fakeEngine) Write(_ context.Context, obs domainmemory.Observation) (domainmemory.Record, error) {
	f.writes = append(f.writes, obs)
	return domainmemory.Record{ID: "id", Identity: obs.Identity, Content: obs.Content}, nil
}

func (f *fakeEngine) Query(context.Context, domainmemory.Query) ([]domainmemory.Record, error) {
	return nil, nil
}

func (f *fakeEngine) List(context.Context, string) ([]domainmemory.Record, error) { return nil, nil }
func (f *fakeEngine) Forget(context.Context, string) error                        { return nil }

type fakeEngineSource struct{ engine *fakeEngine }

func (f fakeEngineSource) Get(name string) (domainmemory.MemoryEngine, bool) {
	if name != "builtin" {
		return nil, false
	}
	return f.engine, true
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time                         { return c.now }
func (c testClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func newTestCurator(t *testing.T, conv *fakeConversations, llm *fakeLLM, engine *fakeEngine) *Curator {
	t.Helper()
	c := New(time.Hour, "builtin")
	c.Bind(curator.Registrar{
		Conversations: conv,
		LLM:           llm,
		MemoryEngines: fakeEngineSource{engine: engine},
		Clock:         testClock{now: time.Unix(1000, 0)},
	})
	return c
}

// --- tests ---------------------------------------------------------------

func TestCheckReportsFalseWithNoRecentSessions(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{sessions: map[string]time.Time{}}
	c := newTestCurator(t, conv, &fakeLLM{}, &fakeEngine{})

	ok, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if ok {
		t.Error("Check() = true, want false: no recent sessions")
	}
}

func TestCheckReportsTrueWithARecentSession(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{sessions: map[string]time.Time{"s1": time.Unix(1000, 0)}}
	c := newTestCurator(t, conv, &fakeLLM{}, &fakeEngine{})

	ok, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if !ok {
		t.Error("Check() = false, want true: a session is recently active")
	}
}

func TestPassExtractsAndWritesObservations(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0)},
		messages: map[string][]curator.ConversationMessage{
			"s1": {{Role: "user", Content: "I prefer tabs over spaces"}},
		},
	}
	llm := &fakeLLM{responses: []string{`["prefers tabs over spaces"]`}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Type != ActionExtracted {
		t.Fatalf("Actions = %#v, want one %s", res.Actions, ActionExtracted)
	}
	if len(engine.writes) != 1 {
		t.Fatalf("writes = %#v, want exactly 1", engine.writes)
	}
	got := engine.writes[0]
	if got.Identity != "s1" {
		t.Errorf("Identity = %q, want the session id, not a user/cross-session identity", got.Identity)
	}
	if got.Content != "prefers tabs over spaces" {
		t.Errorf("Content = %q, want the extracted fact verbatim", got.Content)
	}
}

func TestPassTreatsUnparseableResponseAsNothingExtracted(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0)},
		messages: map[string][]curator.ConversationMessage{"s1": {{Role: "user", Content: "hello"}}},
	}
	llm := &fakeLLM{responses: []string{"not json at all"}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil: an unparseable response is not a pass failure", err)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("Actions = %#v, want none", res.Actions)
	}
	if len(engine.writes) != 0 {
		t.Fatalf("writes = %#v, want none", engine.writes)
	}
}

func TestPassEmptyArrayExtractsNothing(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0)},
		messages: map[string][]curator.ConversationMessage{"s1": {{Role: "user", Content: "hi"}}},
	}
	llm := &fakeLLM{responses: []string{`[]`}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("Actions = %#v, want none: nothing in the excerpt was worth keeping", res.Actions)
	}
}

func TestPassCapsObservationsPerSession(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0)},
		messages: map[string][]curator.ConversationMessage{"s1": {{Role: "user", Content: "lots of facts"}}},
	}
	llm := &fakeLLM{responses: []string{`["a","b","c","d","e","f","g"]`}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	if _, err := c.Pass(context.Background(), curator.PassInput{}); err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(engine.writes) != maxObservationsPerSession {
		t.Fatalf("writes = %d, want capped at %d", len(engine.writes), maxObservationsPerSession)
	}
}

func TestPassReviewsMultipleSessionsIndependently(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0), "s2": time.Unix(1000, 0)},
		messages: map[string][]curator.ConversationMessage{
			"s1": {{Role: "user", Content: "fact for s1"}},
			"s2": {{Role: "user", Content: "fact for s2"}},
		},
	}
	llm := &fakeLLM{responses: []string{`["fact one"]`, `["fact two"]`}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("Actions = %#v, want 2 (one per session)", res.Actions)
	}
	if len(engine.writes) != 2 {
		t.Fatalf("writes = %#v, want 2", engine.writes)
	}
	identities := map[string]bool{}
	for _, w := range engine.writes {
		identities[w.Identity] = true
	}
	if !identities["s1"] || !identities["s2"] {
		t.Errorf("writes scoped to identities %v, want both s1 and s2", identities)
	}
}

func TestPassUnknownEngineIsAnError(t *testing.T) {
	t.Parallel()
	c := New(time.Hour, "does-not-exist")
	c.Bind(curator.Registrar{
		Conversations: &fakeConversations{},
		MemoryEngines: fakeEngineSource{engine: &fakeEngine{}},
		Clock:         testClock{now: time.Unix(1000, 0)},
	})

	if _, err := c.Pass(context.Background(), curator.PassInput{}); err == nil {
		t.Fatal("Pass() with an unregistered engine name = nil, want error")
	}
}

func TestEffectiveSinceDefaultsToLookbackWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	got := effectiveSince(time.Time{}, now)
	want := now.Add(-defaultLookback)
	if !got.Equal(want) {
		t.Errorf("effectiveSince(zero, now) = %v, want %v", got, want)
	}
}

func TestEffectiveSincePassesThroughAnExplicitValue(t *testing.T) {
	t.Parallel()
	explicit := time.Unix(500, 0)
	got := effectiveSince(explicit, time.Unix(1_000_000, 0))
	if !got.Equal(explicit) {
		t.Errorf("effectiveSince(explicit, now) = %v, want %v unchanged", got, explicit)
	}
}

func TestManifestShape(t *testing.T) {
	t.Parallel()
	c := New(15*time.Minute, "builtin")
	m := c.Manifest()
	if !m.OnInput {
		t.Error("Manifest().OnInput = false, want true: wakes on completed chat turns")
	}
	if !m.Conversations {
		t.Error("Manifest().Conversations = false, want true")
	}
	if m.MemoryEngine != "builtin" {
		t.Errorf("Manifest().MemoryEngine = %q, want %q", m.MemoryEngine, "builtin")
	}
	if len(m.Tools) != 0 {
		t.Errorf("Manifest().Tools = %v, want none: extraction is a plain completion, not tool-calling", m.Tools)
	}
	if !m.Skills {
		// deliberately NOT checking true -- documents the negative
	}
	if m.Skills {
		t.Error("Manifest().Skills = true, want false")
	}
}

func TestRegistersAndRunsThroughTheRegistry(t *testing.T) {
	t.Parallel()
	r := curator.NewRegistry(curator.Registrar{
		Conversations: &fakeConversations{},
		LLM:           &fakeLLM{},
		MemoryEngines: fakeEngineSource{engine: &fakeEngine{}},
	})
	c := New(time.Hour, "builtin")
	if err := r.Register(c); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("registry Start() = %v, want nil", err)
	}
	if health := r.Health(context.Background()); health[Name].Status != curator.HealthHealthy {
		t.Errorf("Health() = %v, want healthy", health)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("registry Stop() = %v, want nil", err)
	}
}

var errLLM = errors.New("model unavailable")

func TestPassPropagatesALLMFailure(t *testing.T) {
	// Unlike an unparseable response, the model call itself failing (the
	// provider is down) is a real Pass error, not a "nothing to extract"
	// outcome.
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0)},
		messages: map[string][]curator.ConversationMessage{"s1": {{Role: "user", Content: "hi"}}},
	}
	llm := &fakeLLM{err: errLLM}
	c := newTestCurator(t, conv, llm, &fakeEngine{})

	if _, err := c.Pass(context.Background(), curator.PassInput{}); !errors.Is(err, errLLM) {
		t.Fatalf("Pass() error = %v, want it to wrap the LLM failure", err)
	}
}
