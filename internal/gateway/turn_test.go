package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/tools"
)

type turnTestModel struct {
	prepared   *turnTestPreparedModel
	prepareErr error
	gotExtra   []tools.ToolEntry
}

func (m *turnTestModel) Prepare(_ context.Context, req TurnPrepareContext) (PreparedTurnModel, error) {
	m.gotExtra = append([]tools.ToolEntry(nil), req.Extra...)
	if m.prepareErr != nil {
		return nil, m.prepareErr
	}
	return m.prepared, nil
}

type turnTestPreparedModel struct {
	reply       string
	toolInfo    []ToolSummary
	toolTokens  int
	generateN   int
	generateErr error
	generate    func(context.Context) (string, error)
	// generateStream lets a test drive the reported progress  --  text and
	// tool activity  --  rather than only the returned reply.
	generateStream func(TurnStream) (string, error)
	lastRequest    TurnModelRequest
}

func (m *turnTestPreparedModel) ToolSummaries() []ToolSummary {
	return append([]ToolSummary(nil), m.toolInfo...)
}

func (m *turnTestPreparedModel) ToolSchemaTokens() int {
	return m.toolTokens
}

func (m *turnTestPreparedModel) Generate(ctx context.Context, req TurnModelRequest, stream TurnStream) (string, error) {
	m.generateN++
	m.lastRequest = req
	if m.generateStream != nil {
		return m.generateStream(stream)
	}
	if m.generate != nil {
		return m.generate(ctx)
	}
	if m.generateErr != nil {
		return "", m.generateErr
	}
	if stream != nil {
		stream.Delta(m.reply)
	}
	return m.reply, nil
}

type turnTestEvents struct {
	published []events.Event
}

func (b *turnTestEvents) Publish(event events.Event) {
	b.published = append(b.published, event)
}

func TestTurnRunnerPersistsOneTurnAndPublishesCompletion(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)

	prepared := &turnTestPreparedModel{
		reply:      "answer",
		toolInfo:   []ToolSummary{{Name: "turn_tool", Description: "test tool"}},
		toolTokens: 12,
	}
	model := &turnTestModel{prepared: prepared}
	eventsBus := &turnTestEvents{}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    model,
		Bus:      eventsBus,
		BotUser:  "archie",
		Channel:  "telegram",
		Operator: "Sam",
		Log:      slog.New(slog.DiscardHandler),
	})

	var streamed string
	reply, err := runner.Run(context.Background(), Message{
		From:      "user",
		ChannelID: "chat-1",
		SourceID:  "source-1",
		Text:      "hello",
	}, DeltaFunc(func(delta string) { streamed += delta }))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reply != "answer" || streamed != "answer" {
		t.Fatalf("reply = %q, streamed = %q", reply, streamed)
	}

	history, err := store.RecentMessages(context.Background(), "chat-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if history[0].Text != "hello" || history[1].Text != "answer" {
		t.Fatalf("history = %#v, want inbound then assistant", history)
	}
	if prepared.generateN != 1 {
		t.Fatalf("Generate calls = %d, want 1", prepared.generateN)
	}
	if len(model.gotExtra) == 0 {
		t.Fatal("Prepare received no session tools")
	}
	if len(prepared.lastRequest.Messages) < 2 || prepared.lastRequest.Messages[0].Role != "system" {
		t.Fatalf("model request messages = %#v, want system prompt and history", prepared.lastRequest.Messages)
	}
	if len(eventsBus.published) != 1 || eventsBus.published[0].Kind != events.KindTurnCompleted {
		t.Fatalf("published events = %#v, want one turn_completed event", eventsBus.published)
	}
	turn, ok, err := runner.Ledger.GetTurn(context.Background(), CanonicalTurnID("chat-1", "source-1"))
	if err != nil || !ok {
		t.Fatalf("GetTurn() = %#v, %v, %v; want completed turn", turn, ok, err)
	}
	if turn.Status != TurnStatusCompleted || turn.ResponseText != "answer" || turn.InputMessageID == "" || turn.AssistantMessageID == "" {
		t.Fatalf("persisted turn = %#v, want completed IDs and response", turn)
	}
}

func TestTurnRunnerGenerationFailureLeavesInboundWithoutReply(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	prepared := &turnTestPreparedModel{generateErr: errors.New("provider unavailable")}
	bus := &turnTestEvents{}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    &turnTestModel{prepared: prepared},
		Bus:      bus,
		BotUser:  "archie",
		Channel:  "telegram",
	})

	_, err := runner.Run(context.Background(), Message{
		From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("Run() error = %v, want provider error", err)
	}
	history, err := store.RecentMessages(context.Background(), "chat-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) != 1 || history[0].Text != "hello" {
		t.Fatalf("history after failed generation = %#v, want inbound only", history)
	}
	if len(bus.published) != 0 {
		t.Fatalf("published events = %#v, want none", bus.published)
	}
	turn, ok, err := runner.Ledger.GetTurn(context.Background(), CanonicalTurnID("chat-1", "source-1"))
	if err != nil || !ok || turn.Status != TurnStatusFailed || turn.Error == "" {
		t.Fatalf("failed turn = %#v, %v, %v; want persisted failure", turn, ok, err)
	}
}

func TestTurnRunnerCancellationPersistsCancelledState(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared := &turnTestPreparedModel{generate: func(context.Context) (string, error) {
		cancel()
		return "", context.Canceled
	}}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    &turnTestModel{prepared: prepared},
		BotUser:  "archie",
		Channel:  "telegram",
	})

	_, err := runner.Run(runCtx, Message{
		From: "user", ChannelID: "chat-cancel", SourceID: "source-cancel", Text: "hello",
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	turn, ok, err := runner.Ledger.GetTurn(context.Background(), CanonicalTurnID("chat-cancel", "source-cancel"))
	if err != nil || !ok || turn.Status != TurnStatusCancelled {
		t.Fatalf("cancelled turn = %#v, %v, %v; want persisted cancellation", turn, ok, err)
	}
}

func TestTurnRunnerRecoversTurnOwnedByPreviousProcess(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	turnID := CanonicalTurnID("chat-recover", "source-recover")
	ledger, ok := store.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", store)
	}
	if _, claim, err := ledger.ClaimTurn(context.Background(), TurnRecord{
		TurnID: turnID, SessionID: "chat-recover", SourceID: "source-recover",
		OwnerID: "previous-process", Status: TurnStatusAccepted,
	}); err != nil || claim != TurnClaimOwned {
		t.Fatalf("seed ClaimTurn() = %q, %v", claim, err)
	}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    &turnTestModel{prepared: &turnTestPreparedModel{reply: "recovered"}},
		OwnerID:  "new-process",
		BotUser:  "archie",
		Channel:  "telegram",
	})

	got, err := runner.Run(context.Background(), Message{
		From: "user", ChannelID: "chat-recover", SourceID: "source-recover", Text: "resume me",
	}, nil)
	if err != nil || got != "recovered" {
		t.Fatalf("Run() = %q, %v; want recovered response", got, err)
	}
	turn, ok, err := runner.Ledger.GetTurn(context.Background(), turnID)
	if err != nil || !ok || turn.Status != TurnStatusCompleted || turn.Attempt != 2 || turn.OwnerID != "new-process" {
		t.Fatalf("recovered turn = %#v, %v, %v; want attempt 2 completed by new process", turn, ok, err)
	}
}

func TestTurnRunnerPreparationFailureLeavesInboundWithoutReply(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    &turnTestModel{prepareErr: errors.New("tool setup failed")},
		BotUser:  "archie",
		Channel:  "telegram",
	})

	_, err := runner.Run(context.Background(), Message{
		From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "tool setup failed") {
		t.Fatalf("Run() error = %v, want tool setup error", err)
	}
	history, err := store.RecentMessages(context.Background(), "chat-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) != 1 || history[0].Text != "hello" {
		t.Fatalf("history after failed preparation = %#v, want inbound only", history)
	}
	turn, ok, err := runner.Ledger.GetTurn(context.Background(), CanonicalTurnID("chat-1", "source-1"))
	if err != nil || !ok || turn.Status != TurnStatusFailed || turn.Error == "" {
		t.Fatalf("failed preparation turn = %#v, %v, %v; want persisted failure", turn, ok, err)
	}
}

func TestTurnRunnerReplaysCompletedDuplicateOutsideRecentWindow(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	prepared := &turnTestPreparedModel{reply: "answer"}
	model := &turnTestModel{prepared: prepared}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    model,
		BotUser:  "archie",
		Channel:  "telegram",
	})
	msg := Message{From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello"}

	if _, err := runner.Run(context.Background(), msg, nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	for i := range 110 {
		if err := store.SaveMessage(context.Background(), "chat-1", Message{
			From: "user", SourceID: fmt.Sprintf("later-%d", i), Text: "later",
		}); err != nil {
			t.Fatalf("SaveMessage(%d) error = %v", i, err)
		}
	}

	reply, err := runner.Run(context.Background(), msg, nil)
	if err != nil {
		t.Fatalf("duplicate Run() error = %v", err)
	}
	if reply != "answer" {
		t.Fatalf("duplicate reply = %q, want answer", reply)
	}
	if model.prepared.generateN != 1 {
		t.Fatalf("Generate calls = %d, want 1", model.prepared.generateN)
	}
}

func TestTurnRunnerReplaysCompletedDuplicateWithoutGenerating(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	prepared := &turnTestPreparedModel{reply: "answer"}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    &turnTestModel{prepared: prepared},
		BotUser:  "archie",
		Channel:  "telegram",
	})
	msg := Message{From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello"}

	if _, err := runner.Run(context.Background(), msg, nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	var replay string
	reply, err := runner.Run(context.Background(), msg, DeltaFunc(func(delta string) { replay += delta }))
	if err != nil {
		t.Fatalf("duplicate Run() error = %v", err)
	}
	if reply != "answer" || replay != "answer" {
		t.Fatalf("duplicate reply = %q, replay = %q", reply, replay)
	}
	if prepared.generateN != 1 {
		t.Fatalf("Generate calls = %d, want 1", prepared.generateN)
	}
	history, err := store.RecentMessages(context.Background(), "chat-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history length after duplicate = %d, want 2", len(history))
	}
}
