package sessioncurator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// --- fakes -------------------------------------------------------------

type fakeConversations struct {
	sessions map[string]time.Time // id -> last active
	agents   map[string]string    // id -> the agent that served it
	messages map[string][]curator.ConversationMessage
}

func (f *fakeConversations) RecentSessions(_ context.Context, since time.Time) ([]curator.SessionSummary, error) {
	var out []curator.SessionSummary
	for id, at := range f.sessions {
		if at.Before(since) {
			continue
		}
		out = append(out, curator.SessionSummary{ID: id, AgentID: f.agents[id], LastActive: at})
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

// newFakeConversations builds the single-session fixture every write-path
// case starts from: session "s1", served by agent "winter", with the given
// message tail.
func newFakeConversations(msgs ...curator.ConversationMessage) *fakeConversations {
	return &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0)},
		agents:   map[string]string{"s1": "winter"},
		messages: map[string][]curator.ConversationMessage{"s1": msgs},
	}
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

// fakeEngine records what the curator asked it to create. domain/memory's
// own fake engine is unexported in its package's test files, so this one is
// minimal on purpose: the read half exists only to satisfy MemoryEngine.
type fakeEngine struct {
	created   []domainmemory.NewRecord
	createErr error
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

func (f *fakeEngine) Create(_ context.Context, in domainmemory.NewRecord) (domainmemory.Record, error) {
	if f.createErr != nil {
		return domainmemory.Record{}, f.createErr
	}
	f.created = append(f.created, in)
	return domainmemory.Record{
		ID:         domainmemory.RecordID(fmt.Sprintf("r%d", len(f.created))),
		Scope:      in.Scope,
		Kind:       in.Kind,
		Content:    in.Content,
		Author:     in.Author,
		OriginUser: in.OriginUser,
		Source:     in.Source,
	}, nil
}

func (f *fakeEngine) Get(context.Context, domainmemory.Scope, domainmemory.RecordID) (domainmemory.Record, error) {
	return domainmemory.Record{}, domainmemory.ErrNotFound
}

func (f *fakeEngine) Query(context.Context, domainmemory.Query) ([]domainmemory.Record, error) {
	return nil, nil
}

func (f *fakeEngine) List(context.Context, domainmemory.Scope) ([]domainmemory.Record, error) {
	return nil, nil
}

func (f *fakeEngine) Update(context.Context, domainmemory.RecordUpdate) (domainmemory.Record, error) {
	return domainmemory.Record{}, domainmemory.ErrNotFound
}

func (f *fakeEngine) Forget(context.Context, domainmemory.Scope, domainmemory.RecordID) error {
	return nil
}

func (f *fakeEngine) Revisions(context.Context, domainmemory.Scope, domainmemory.RecordID) ([]domainmemory.Revision, error) {
	return nil, nil
}

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
	conv := newFakeConversations(curator.ConversationMessage{Role: "user", SenderID: "u-42", Content: "I prefer tabs over spaces"})
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
	if want := time.Unix(1000, 0); !res.Actions[0].At.Equal(want) {
		t.Errorf("Actions[0].At = %v, want %v (from the curator's clock, not zero)", res.Actions[0].At, want)
	}
	if len(engine.created) != 1 {
		t.Fatalf("created = %#v, want exactly 1", engine.created)
	}
	got := engine.created[0]
	// The session id is not a scope: what the curator extracted is a fact
	// about one person as seen by one agent, and agent-user is the only
	// scope that says so.
	if want := (domainmemory.Scope{Kind: domainmemory.ScopeAgentUser, Agent: "winter", User: "u-42"}); got.Scope != want {
		t.Errorf("Scope = %v, want %v", got.Scope, want)
	}
	if got.Content != "prefers tabs over spaces" {
		t.Errorf("Content = %q, want the extracted fact verbatim", got.Content)
	}
}

// TestPassWritesExtractedFactsToAgentUserScope pins the address and
// provenance of every curator write: a later chat turn reads back
// agent-user memory for its own agent and user, and only a record written
// there is visible to it.
func TestPassWritesExtractedFactsToAgentUserScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		messages     []curator.ConversationMessage
		response     string
		wantContents []string
	}{
		{
			name:         "one user sender",
			messages:     []curator.ConversationMessage{{Role: "user", SenderID: "u-42", Content: "I prefer tabs"}},
			response:     `["prefers tabs over spaces"]`,
			wantContents: []string{"prefers tabs over spaces"},
		},
		{
			name: "the same sender across several messages is still one participant",
			messages: []curator.ConversationMessage{
				{Role: "user", SenderID: "u-42", Content: "I prefer tabs"},
				{Role: "assistant", Content: "noted"},
				{Role: "user", SenderID: "u-42", Content: "and vim bindings"},
			},
			response:     `["prefers tabs over spaces"]`,
			wantContents: []string{"prefers tabs over spaces"},
		},
		{
			name: "an assistant sender beside one identified user sender is still one participant",
			messages: []curator.ConversationMessage{
				{Role: "user", SenderID: "u-42", Content: "I prefer tabs"},
				{Role: "assistant", SenderID: "winter", Content: "noted"},
			},
			response:     `["prefers tabs over spaces"]`,
			wantContents: []string{"prefers tabs over spaces"},
		},
		{
			name:         "one write per extracted fact",
			messages:     []curator.ConversationMessage{{Role: "user", SenderID: "u-42", Content: "lots of facts"}},
			response:     `["fact a","fact b","fact c"]`,
			wantContents: []string{"fact a", "fact b", "fact c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			engine := &fakeEngine{}
			c := newTestCurator(t, newFakeConversations(tt.messages...), &fakeLLM{responses: []string{tt.response}}, engine)

			res, err := c.Pass(context.Background(), curator.PassInput{})
			if err != nil {
				t.Fatalf("Pass() = %v, want nil", err)
			}
			wantScope := domainmemory.Scope{Kind: domainmemory.ScopeAgentUser, Agent: "winter", User: "u-42"}
			if len(engine.created) != len(tt.wantContents) {
				t.Fatalf("created = %#v, want %d record(s)", engine.created, len(tt.wantContents))
			}
			for i, want := range tt.wantContents {
				got := engine.created[i]
				if got.Scope != wantScope {
					t.Errorf("created[%d].Scope = %v, want %v", i, got.Scope, wantScope)
				}
				if got.Kind != "note" {
					t.Errorf("created[%d].Kind = %q, want %q", i, got.Kind, "note")
				}
				if got.Author != Name {
					t.Errorf("created[%d].Author = %q, want the producer's own name %q", i, got.Author, Name)
				}
				if got.OriginUser != domainmemory.IdentityID("u-42") {
					t.Errorf("created[%d].OriginUser = %q, want the participant %q", i, got.OriginUser, "u-42")
				}
				if got.Source != "s1" {
					t.Errorf("created[%d].Source = %q, want the session id %q", i, got.Source, "s1")
				}
				if got.Content != want {
					t.Errorf("created[%d].Content = %q, want %q", i, got.Content, want)
				}
			}
			if len(res.Actions) != 1 || res.Actions[0].Type != ActionExtracted {
				t.Errorf("Actions = %#v, want one %s", res.Actions, ActionExtracted)
			}
		})
	}
}

// TestPassSkipsSessionsWithoutASingleParticipant pins the fail-closed rule.
// Every case's model response offers a fact, so a write here could only come
// from a guessed participant.
func TestPassSkipsSessionsWithoutASingleParticipant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		messages   []curator.ConversationMessage
		wantReason string
	}{
		{
			name:       "zero senders: a dashboard session carries no per-person identity",
			messages:   []curator.ConversationMessage{{Role: "user", Content: "hello"}},
			wantReason: "no single participant: 0 distinct sender(s), 1 unidentified user message(s)",
		},
		{
			name: "an identified sender beside an unidentified user message: the excerpt's provenance is unknown",
			messages: []curator.ConversationMessage{
				{Role: "user", SenderID: "u-42", Content: "I prefer tabs"},
				{Role: "user", Content: "a message whose channel carries no sender id"},
			},
			wantReason: "no single participant: 1 distinct sender(s), 1 unidentified user message(s)",
		},
		{
			name: "several distinct senders: a group chat",
			messages: []curator.ConversationMessage{
				{Role: "user", SenderID: "u-1", Content: "hi"},
				{Role: "user", SenderID: "u-2", Content: "hello"},
			},
			wantReason: "no single participant: 2 distinct sender(s)",
		},
		{
			name:       "a sender present only on assistant messages does not count",
			messages:   []curator.ConversationMessage{{Role: "assistant", SenderID: "winter", Content: "hi"}},
			wantReason: "no single participant: 0 distinct sender(s)",
		},
		{
			name: "empty senders among the user messages do not create a participant",
			messages: []curator.ConversationMessage{
				{Role: "user", Content: "hello"},
				{Role: "user", Content: "still nobody"},
			},
			wantReason: "no single participant: 0 distinct sender(s), 2 unidentified user message(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			engine := &fakeEngine{}
			c := newTestCurator(t, newFakeConversations(tt.messages...), &fakeLLM{responses: []string{`["a fact worth keeping"]`}}, engine)

			res, err := c.Pass(context.Background(), curator.PassInput{})
			if err != nil {
				t.Fatalf("Pass() = %v, want nil: an unattributable session is skipped, not a pass failure", err)
			}
			if len(engine.created) != 0 {
				t.Fatalf("created = %#v, want none: the session has no single participant to write for", engine.created)
			}
			if len(res.Actions) != 1 {
				t.Fatalf("Actions = %#v, want exactly one skip", res.Actions)
			}
			got := res.Actions[0]
			if got.Type != ActionSkipped {
				t.Errorf("Actions[0].Type = %q, want %q", got.Type, ActionSkipped)
			}
			if got.Detail != "s1" {
				t.Errorf("Actions[0].Detail = %q, want the session id %q", got.Detail, "s1")
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Actions[0].Reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if want := time.Unix(1000, 0); !got.At.Equal(want) {
				t.Errorf("Actions[0].At = %v, want %v (from the curator's clock)", got.At, want)
			}
		})
	}
}

func TestPassTreatsUnparseableResponseAsNothingExtracted(t *testing.T) {
	t.Parallel()
	conv := newFakeConversations(curator.ConversationMessage{Role: "user", SenderID: "u-42", Content: "hello"})
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
	if len(engine.created) != 0 {
		t.Fatalf("created = %#v, want none", engine.created)
	}
}

func TestPassEmptyArrayExtractsNothing(t *testing.T) {
	t.Parallel()
	conv := newFakeConversations(curator.ConversationMessage{Role: "user", SenderID: "u-42", Content: "hi"})
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

func TestPassTreatsASessionWithNoMessagesAsNothingExtracted(t *testing.T) {
	t.Parallel()
	conv := newFakeConversations()
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, &fakeLLM{responses: []string{`["a fact worth keeping"]`}}, engine)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("Actions = %#v, want none: a session with no messages yields nothing to extract", res.Actions)
	}
	if len(engine.created) != 0 {
		t.Fatalf("created = %#v, want none", engine.created)
	}
}

func TestPassCapsObservationsPerSession(t *testing.T) {
	t.Parallel()
	conv := newFakeConversations(curator.ConversationMessage{Role: "user", SenderID: "u-42", Content: "lots of facts"})
	llm := &fakeLLM{responses: []string{`["a","b","c","d","e","f","g"]`}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	if _, err := c.Pass(context.Background(), curator.PassInput{}); err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(engine.created) != maxObservationsPerSession {
		t.Fatalf("created = %d, want capped at %d", len(engine.created), maxObservationsPerSession)
	}
	// The cap truncates the tail, and keeps the order the model returned.
	want := []string{"a", "b", "c", "d", "e"}
	for i, content := range want {
		if engine.created[i].Content != content {
			t.Errorf("created[%d].Content = %q, want %q", i, engine.created[i].Content, content)
		}
	}
}

func TestPassReviewsMultipleSessionsIndependently(t *testing.T) {
	t.Parallel()
	conv := &fakeConversations{
		sessions: map[string]time.Time{"s1": time.Unix(1000, 0), "s2": time.Unix(1000, 0)},
		agents:   map[string]string{"s1": "winter", "s2": "spring"},
		messages: map[string][]curator.ConversationMessage{
			"s1": {{Role: "user", SenderID: "u-1", Content: "fact for s1"}},
			"s2": {{Role: "user", SenderID: "u-2", Content: "fact for s2"}},
		},
	}
	// One response for both sessions: the map iteration order above is not
	// deterministic, so nothing here may depend on which session is asked
	// first -- attribution is asserted through Source.
	llm := &fakeLLM{responses: []string{`["a fact"]`}}
	engine := &fakeEngine{}
	c := newTestCurator(t, conv, llm, engine)

	res, err := c.Pass(context.Background(), curator.PassInput{})
	if err != nil {
		t.Fatalf("Pass() = %v, want nil", err)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("Actions = %#v, want 2 (one per session)", res.Actions)
	}
	if len(engine.created) != 2 {
		t.Fatalf("created = %#v, want 2", engine.created)
	}
	scopes := map[string]domainmemory.Scope{}
	for _, rec := range engine.created {
		scopes[rec.Source] = rec.Scope
	}
	// Each session's own agent addresses its scope, so a session summary
	// whose agent id is dropped or swapped writes into another agent's
	// memory.
	for session, want := range map[string]domainmemory.Scope{
		"s1": {Kind: domainmemory.ScopeAgentUser, Agent: "winter", User: "u-1"},
		"s2": {Kind: domainmemory.ScopeAgentUser, Agent: "spring", User: "u-2"},
	} {
		if got := scopes[session]; got != want {
			t.Errorf("scope of the write sourced from %s = %v, want %v", session, got, want)
		}
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
	// deliberately NOT checking true -- documents the negative
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

var (
	errLLM    = errors.New("model unavailable")
	errCreate = errors.New("engine refused the write")
)

func TestPassPropagatesALLMFailure(t *testing.T) {
	// Unlike an unparseable response, the model call itself failing (the
	// provider is down) is a real Pass error, not a "nothing to extract"
	// outcome.
	t.Parallel()
	conv := newFakeConversations(curator.ConversationMessage{Role: "user", SenderID: "u-42", Content: "hi"})
	llm := &fakeLLM{err: errLLM}
	c := newTestCurator(t, conv, llm, &fakeEngine{})

	if _, err := c.Pass(context.Background(), curator.PassInput{}); !errors.Is(err, errLLM) {
		t.Fatalf("Pass() error = %v, want it to wrap the LLM failure", err)
	}
}

func TestPassPropagatesACreateFailure(t *testing.T) {
	// The engine refusing a write is a real failure, not "nothing to
	// extract": the pass must not report success for a fact it did not
	// persist.
	t.Parallel()
	conv := newFakeConversations(curator.ConversationMessage{Role: "user", SenderID: "u-42", Content: "hi"})
	engine := &fakeEngine{createErr: errCreate}
	c := newTestCurator(t, conv, &fakeLLM{responses: []string{`["a fact"]`}}, engine)

	if _, err := c.Pass(context.Background(), curator.PassInput{}); !errors.Is(err, errCreate) {
		t.Fatalf("Pass() error = %v, want it to wrap the engine's create failure", err)
	}
}
