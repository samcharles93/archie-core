package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	infraMemory "github.com/samcharles93/archie-core/internal/infrastructure/memory"
)

// newMemoryTestEngine builds a real builtin engine so these tests exercise
// the actual scope-key isolation, not a fake that might not mirror it.
func newMemoryTestEngine(t *testing.T) *infraMemory.BuiltinEngine {
	t.Helper()
	return infraMemory.NewBuiltinEngine(t.TempDir(), 0)
}

func telegramIdentity(msg messaging.Message) (domainmemory.IdentityID, bool) {
	if msg.SenderID == "" {
		return "", false
	}
	return domainmemory.IdentityID(msg.SenderID), true
}

func chatMsg(sourceID, channelID, senderID, text string) Inbound {
	return Inbound{Message: messaging.Message{
		SourceID:       sourceID,
		ConversationID: messaging.ConversationID{ChannelID: channelID},
		Sender:         "user",
		SenderID:       senderID,
		Role:           messaging.RoleUser,
		Text:           text,
	}}
}

func newMemoryTestRunner(t *testing.T, engine MemoryStore, userIdentity func(messaging.Message) (domainmemory.IdentityID, bool), botUser string) (*TurnRunner, *turnTestPreparedModel) {
	t.Helper()
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = botUser
	router.InitSessions(store)
	prepared := &turnTestPreparedModel{reply: "ok"}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:       router,
		Sessions:     store,
		Models:       &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas:     NewPersonaRegistry(DefaultPersonas()),
		Model:        &turnTestModel{prepared: prepared},
		BotUser:      botUser,
		Channel:      "telegram",
		Log:          slog.New(slog.DiscardHandler),
		MemoryEngine: engine,
		UserIdentity: userIdentity,
	})
	return runner, prepared
}

// lastSystemPrompt returns the system-prompt message content the model saw
// on its most recent Generate call.
func lastSystemPrompt(prepared *turnTestPreparedModel) string {
	for _, m := range prepared.lastRequest.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

// TestTurnMemoryReadPathAppearsNextTurn is acceptance criterion 1: a record
// created directly through the engine's Store appears in a later turn's
// prompt, with its id.
func TestTurnMemoryReadPathAppearsNextTurn(t *testing.T) {
	engine := newMemoryTestEngine(t)
	runner, prepared := newMemoryTestRunner(t, engine, telegramIdentity, "archie")

	rec, err := engine.Create(context.Background(), domainmemory.NewRecord{
		Scope:   domainmemory.Scope{Kind: domainmemory.ScopeAgentUser, Agent: "archie", User: "alice"},
		Kind:    "note",
		Content: "alice prefers dark roast coffee",
		Author:  "test",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := runner.Run(context.Background(), chatMsg("s1", "chat-1", "alice", "hi"), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	prompt := lastSystemPrompt(prepared)
	if !strings.Contains(prompt, "alice prefers dark roast coffee") {
		t.Fatalf("prompt missing planted record content, prompt = %q", prompt)
	}
	if !strings.Contains(prompt, string(rec.ID)) {
		t.Fatalf("prompt missing record id %q, prompt = %q", rec.ID, prompt)
	}
	if !strings.Contains(prompt, `<memory purpose="durable_context" trust="data">`) {
		t.Fatalf("prompt missing memory block wrapper, prompt = %q", prompt)
	}
}

// TestTurnMemoryIsolatesByUser is acceptance criterion 2: an agent-user
// record for one user is absent from a different user's prompt on the same
// channel and agent, asserted end to end through the turn path.
func TestTurnMemoryIsolatesByUser(t *testing.T) {
	engine := newMemoryTestEngine(t)
	runner, prepared := newMemoryTestRunner(t, engine, telegramIdentity, "archie")

	if _, err := engine.Create(context.Background(), domainmemory.NewRecord{
		Scope:   domainmemory.Scope{Kind: domainmemory.ScopeAgentUser, Agent: "archie", User: "alice"},
		Kind:    "note",
		Content: "alice's secret project is codename phoenix",
		Author:  "test",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := runner.Run(context.Background(), chatMsg("s1", "chat-bob", "bob", "hi"), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	prompt := lastSystemPrompt(prepared)
	if strings.Contains(prompt, "codename phoenix") {
		t.Fatalf("bob's prompt leaked alice's agent-user record, prompt = %q", prompt)
	}
}

// TestTurnMemoryScopeSharing is acceptance criterion 3: agent-scope records
// are visible to both of that agent's users, a global record is visible to a
// second agent, and an agent-user record is not shared.
func TestTurnMemoryScopeSharing(t *testing.T) {
	engine := newMemoryTestEngine(t)
	ctx := context.Background()

	if _, err := engine.Create(ctx, domainmemory.NewRecord{
		Scope: domainmemory.Scope{Kind: domainmemory.ScopeAgent, Agent: "archie"}, Kind: "note",
		Content: "agent-wide fact about archie", Author: "test",
	}); err != nil {
		t.Fatalf("Create(agent) error = %v", err)
	}
	if _, err := engine.Create(ctx, domainmemory.NewRecord{
		Scope: domainmemory.Scope{Kind: domainmemory.ScopeGlobal}, Kind: "note",
		Content: "global fact for every agent", Author: "test",
	}); err != nil {
		t.Fatalf("Create(global) error = %v", err)
	}
	if _, err := engine.Create(ctx, domainmemory.NewRecord{
		Scope: domainmemory.Scope{Kind: domainmemory.ScopeAgentUser, Agent: "archie", User: "alice"}, Kind: "note",
		Content: "relationship-only fact about alice and archie", Author: "test",
	}); err != nil {
		t.Fatalf("Create(agent-user) error = %v", err)
	}

	// alice and bob both talk to archie: both see the agent-wide fact, only
	// alice sees the agent-user fact.
	archieRunner, archiePrepared := newMemoryTestRunner(t, engine, telegramIdentity, "archie")
	if _, err := archieRunner.Run(ctx, chatMsg("s-alice", "chat-alice", "alice", "hi"), nil); err != nil {
		t.Fatalf("Run(alice) error = %v", err)
	}
	alicePrompt := lastSystemPrompt(archiePrepared)
	if !strings.Contains(alicePrompt, "agent-wide fact about archie") {
		t.Fatalf("alice's prompt missing agent-wide fact, prompt = %q", alicePrompt)
	}
	if !strings.Contains(alicePrompt, "relationship-only fact about alice and archie") {
		t.Fatalf("alice's prompt missing her own agent-user fact, prompt = %q", alicePrompt)
	}

	if _, err := archieRunner.Run(ctx, chatMsg("s-bob", "chat-bob", "bob", "hi"), nil); err != nil {
		t.Fatalf("Run(bob) error = %v", err)
	}
	bobPrompt := lastSystemPrompt(archiePrepared)
	if !strings.Contains(bobPrompt, "agent-wide fact about archie") {
		t.Fatalf("bob's prompt missing agent-wide fact, prompt = %q", bobPrompt)
	}
	if strings.Contains(bobPrompt, "relationship-only fact about alice and archie") {
		t.Fatalf("bob's prompt leaked alice's agent-user fact, prompt = %q", bobPrompt)
	}

	// A second agent sees the global fact but not the first agent's
	// agent-scope fact.
	otherRunner, otherPrepared := newMemoryTestRunner(t, engine, telegramIdentity, "other-agent")
	if _, err := otherRunner.Run(ctx, chatMsg("s-carol", "chat-carol", "carol", "hi"), nil); err != nil {
		t.Fatalf("Run(other agent) error = %v", err)
	}
	otherPrompt := lastSystemPrompt(otherPrepared)
	if !strings.Contains(otherPrompt, "global fact for every agent") {
		t.Fatalf("second agent's prompt missing global fact, prompt = %q", otherPrompt)
	}
	if strings.Contains(otherPrompt, "agent-wide fact about archie") {
		t.Fatalf("second agent's prompt leaked the first agent's agent-scope fact, prompt = %q", otherPrompt)
	}
}

// failingMemoryStore always errors.
type failingMemoryStore struct{}

func (failingMemoryStore) Query(context.Context, domainmemory.Query) ([]domainmemory.Record, error) {
	return nil, errors.New("engine unavailable")
}

// panickingMemoryStore always panics.
type panickingMemoryStore struct{}

func (panickingMemoryStore) Query(context.Context, domainmemory.Query) ([]domainmemory.Record, error) {
	panic("boom")
}

// TestTurnMemoryFailingEngineDegradesSilently is acceptance criterion 6 (the
// failing half): a failing engine completes the turn with no memory block
// and no surfaced error.
func TestTurnMemoryFailingEngineDegradesSilently(t *testing.T) {
	runner, prepared := newMemoryTestRunner(t, failingMemoryStore{}, telegramIdentity, "archie")

	reply, err := runner.Run(context.Background(), chatMsg("s1", "chat-1", "alice", "hi"), nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want turn to complete despite the engine failure", err)
	}
	if reply != "ok" {
		t.Fatalf("reply = %q, want ok", reply)
	}
	if strings.Contains(lastSystemPrompt(prepared), "<memory ") {
		t.Fatalf("prompt has a memory block despite a failing engine, prompt = %q", lastSystemPrompt(prepared))
	}
}

// TestTurnMemoryPanickingEngineDegradesSilently is acceptance criterion 6
// (the panic half): a panicking engine/provider completes the turn with no
// memory block and no surfaced error or crash.
func TestTurnMemoryPanickingEngineDegradesSilently(t *testing.T) {
	runner, prepared := newMemoryTestRunner(t, panickingMemoryStore{}, telegramIdentity, "archie")

	reply, err := runner.Run(context.Background(), chatMsg("s1", "chat-1", "alice", "hi"), nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want turn to complete despite the engine panic", err)
	}
	if reply != "ok" {
		t.Fatalf("reply = %q, want ok", reply)
	}
	if strings.Contains(lastSystemPrompt(prepared), "<memory ") {
		t.Fatalf("prompt has a memory block despite a panicking engine, prompt = %q", lastSystemPrompt(prepared))
	}
}

// TestTurnMemoryNoResolvableUserGetsGlobalAndAgentOnly is acceptance
// criterion 7 (the fail-closed half): a channel whose UserIdentity resolver
// is nil (the dashboard/"web" channel) renders only global and agent scopes,
// never a user-scoped or agent-user record.
func TestTurnMemoryNoResolvableUserGetsGlobalAndAgentOnly(t *testing.T) {
	engine := newMemoryTestEngine(t)
	ctx := context.Background()
	if _, err := engine.Create(ctx, domainmemory.NewRecord{
		Scope: domainmemory.Scope{Kind: domainmemory.ScopeAgent, Agent: "archie"}, Kind: "note",
		Content: "agent-scope fact", Author: "test",
	}); err != nil {
		t.Fatalf("Create(agent) error = %v", err)
	}
	if _, err := engine.Create(ctx, domainmemory.NewRecord{
		Scope: domainmemory.Scope{Kind: domainmemory.ScopeGlobal}, Kind: "note",
		Content: "global-scope fact", Author: "test",
	}); err != nil {
		t.Fatalf("Create(global) error = %v", err)
	}
	if _, err := engine.Create(ctx, domainmemory.NewRecord{
		Scope: domainmemory.Scope{Kind: domainmemory.ScopeUser, User: "webhook-user"}, Kind: "note",
		Content: "user-scope fact that must never leak to a resolverless channel", Author: "test",
	}); err != nil {
		t.Fatalf("Create(user) error = %v", err)
	}

	// nil UserIdentity: exactly what the dashboard/"web" channel wires.
	runner, prepared := newMemoryTestRunner(t, engine, nil, "archie")
	msg := chatMsg("s1", "chat-1", "webhook-user", "hi")
	if _, err := runner.Run(ctx, msg, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	prompt := lastSystemPrompt(prepared)
	if !strings.Contains(prompt, "agent-scope fact") || !strings.Contains(prompt, "global-scope fact") {
		t.Fatalf("prompt missing agent/global scope facts, prompt = %q", prompt)
	}
	if strings.Contains(prompt, "user-scope fact that must never leak") {
		t.Fatalf("prompt leaked a user-scope record despite no resolvable user identity, prompt = %q", prompt)
	}
}

// TestRenderMemoryRecordsCapsLoudly is acceptance criterion 8's read-side
// analogue: a set of records whose rendering would exceed the byte cap is
// bounded, not silently corrupted -- some records are dropped and a warning
// is logged, rather than the block being cut off mid-line.
func TestRenderMemoryRecordsCapsLoudly(t *testing.T) {
	var records []domainmemory.Record
	for i := range 500 {
		records = append(records, domainmemory.Record{
			ID:      domainmemory.RecordID(fmt.Sprintf("rec-%d", i)),
			Scope:   domainmemory.Scope{Kind: domainmemory.ScopeGlobal},
			Kind:    "note",
			Content: strings.Repeat("x", 100),
		})
	}

	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, nil))
	got := renderMemoryRecords(records, log)

	if len(got) > memoryBlockByteCap {
		t.Fatalf("rendered block = %d bytes, want <= %d", len(got), memoryBlockByteCap)
	}
	if !strings.Contains(logged.String(), "exceeded its render cap") {
		t.Fatalf("expected a loud log line on cap overflow, got %q", logged.String())
	}
	// Every rendered line must be complete: no record content cut off
	// mid-line, which would be silent corruption rather than a bounded drop.
	for line := range strings.SplitSeq(got, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, strings.Repeat("x", 100)) {
			t.Fatalf("rendered line looks truncated mid-record: %q", line)
		}
	}
}

// TestRenderMemoryNilStoreIsANoOp guards the nil-engine composition case
// (setupMemoryEngine failed or was never reached): a nil MemoryEngine must
// not panic prepareTurn.
func TestRenderMemoryNilStoreIsANoOp(t *testing.T) {
	got := renderMemory(context.Background(), nil, domainmemory.Subject{AgentID: "archie"}, slog.New(slog.DiscardHandler))
	if got != "" {
		t.Fatalf("renderMemory(nil store) = %q, want empty", got)
	}
}
