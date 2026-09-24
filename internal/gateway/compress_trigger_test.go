package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// compressTriggerModelManager publishes catalog details the way production's
// chatModelManager does, so the turn path can derive a real context budget.
type compressTriggerModelManager struct {
	fakeModelManager
	details map[string]ModelDetails
}

func (m *compressTriggerModelManager) ModelDetails(ref string) (ModelDetails, bool) {
	details, ok := m.details[ref]
	return details, ok
}

// countingReplaceStore counts history replacements, so a test can tell an
// automatic compression apart from a later turn that did not need one.
type countingReplaceStore struct {
	SessionStore
	TurnLedger
	replaces int
}

func (s *countingReplaceStore) ReplaceMessages(ctx context.Context, sessionID string, msgs []messaging.Message, superseded []string) error {
	s.replaces++
	return s.SessionStore.ReplaceMessages(ctx, sessionID, msgs, superseded)
}

// newTriggerStore returns an in-memory store that reports every replacement
// written to it.
func newTriggerStore(t *testing.T) *countingReplaceStore {
	t.Helper()
	inner := NewSessionStoreMemory()
	t.Cleanup(func() { _ = inner.Close() })
	ledger, ok := inner.(TurnLedger)
	if !ok {
		t.Fatal("the session store does not implement TurnLedger")
	}
	return &countingReplaceStore{SessionStore: inner, TurnLedger: ledger}
}

// newTriggerRunner builds the production turn path for one channel: a
// TurnRunner over store, exactly as the composition root wires it.
func newTriggerRunner(t *testing.T, store SessionStore, models ModelManager, router *Router) (*TurnRunner, *turnTestPreparedModel) {
	t.Helper()
	prepared := &turnTestPreparedModel{reply: "ok"}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   models,
		Model:    &turnTestModel{prepared: prepared},
		BotUser:  "archie",
		Channel:  "telegram",
	})
	return runner, prepared
}

// seedLargeHistory fills a session with n bulk messages followed by a short
// tail. The bulk clears a small model's history budget while the tail is what
// survives compression, so the compressed result lands well under the budget
// instead of on top of it.
func seedLargeHistory(t *testing.T, store SessionStore, sessionID string, n int) {
	t.Helper()
	ctx := context.Background()
	body := strings.Repeat("x", 1000) // ~250 tokens
	for i := range n {
		if err := store.SaveMessage(ctx, sessionID, messaging.Message{
			Sender:   "alice",
			Role:     messaging.RoleUser,
			Text:     fmt.Sprintf("bulk m%d %s", i, body),
			SourceID: fmt.Sprintf("bulk-%d", i),
			At:       at(dur(i)),
		}); err != nil {
			t.Fatalf("SaveMessage bulk %d: %v", i, err)
		}
	}
	for i := range 20 {
		if err := store.SaveMessage(ctx, sessionID, messaging.Message{
			Sender:   "alice",
			Role:     messaging.RoleUser,
			Text:     "recent",
			SourceID: fmt.Sprintf("tail-%d", i),
			At:       at(dur(1000 + i)),
		}); err != nil {
			t.Fatalf("SaveMessage tail %d: %v", i, err)
		}
	}
}

// turnIn builds the inbound message a channel hands the turn path.
func turnIn(channelID, sourceID string, offset int) Inbound {
	in := inboundFrom(channelID, "alice", "and another")
	in.Message.SourceID = sourceID
	in.Message.At = at(dur(offset))
	return in
}

// A plain channel message -- not a command -- reaches the trigger through the
// router: the router exists so the messaging path has one place to answer a
// turn, and the compression must happen there rather than in a caller only
// one surface would use.
func TestRoutedChannelMessageCompressesTheSession(t *testing.T) {
	ctx := context.Background()
	store := newTriggerStore(t)
	router, sessionID := routerOn(t, store, "chat-1")
	models := &compressTriggerModelManager{
		models: []string{"local/small"}, activeModel: "local/small",
		details: map[string]ModelDetails{
			"local/small": {Ref: "local/small", ContextWindow: 8192, MaxOutputTokens: 1024},
		},
	}
	runner, _ := newTriggerRunner(t, store, models, router)
	router.LLM = runner.Respond
	router.LLMStream = runner.RespondStream

	seedLargeHistory(t, store, sessionID, 60)

	in := turnIn("chat-1", "tg-routed", 2000)
	in.Message.Text = "what did we decide?"
	if _, err := router.Route(ctx, in); err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	if store.replaces != 1 {
		t.Fatalf("replacements = %d after one routed channel message, want 1", store.replaces)
	}
	stored, err := store.RecentMessages(ctx, sessionID, 1000)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if found := summarised(t, stored); len(found) != 1 {
		t.Fatalf("summaries = %d, want exactly 1", len(found))
	}
}

// summarised returns the session's summary record, which is the one message a
// compression adds.
func summarised(t *testing.T, msgs []messaging.Message) []messaging.Message {
	t.Helper()
	marker := DefaultCompressionConfig().SummaryMarker
	var found []messaging.Message
	for _, m := range msgs {
		if strings.Contains(m.Text, marker) {
			found = append(found, m)
		}
	}
	return found
}

// A channel turn that pushes a session past the model-derived budget must
// compress the session on the same path the whole conversation is served
// from, without the operator typing /compress. This is the trigger the
// compressor shipped without.
func TestTurnCompressesSessionAtTheModelBudget(t *testing.T) {
	ctx := context.Background()
	store := newTriggerStore(t)
	router, sessionID := routerOn(t, store, "chat-1")
	// A small local window: the 60 bulk messages (15k tokens) are far past
	// the history budget an 8k window leaves after prompt and output reserve.
	models := &compressTriggerModelManager{
		models: []string{"local/small"}, activeModel: "local/small",
		details: map[string]ModelDetails{
			"local/small": {Ref: "local/small", ContextWindow: 8192, MaxOutputTokens: 1024},
		},
	}
	runner, prepared := newTriggerRunner(t, store, models, router)

	seedLargeHistory(t, store, sessionID, 60)
	seeded := 80

	if _, err := runner.Run(ctx, turnIn("chat-1", "tg-1", 2000), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	stored, err := store.RecentMessages(ctx, sessionID, 1000)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if store.replaces != 1 {
		t.Fatalf("replacements = %d after a turn that pushed the session past the budget, want exactly 1 "+
			"(history is still %d messages)", store.replaces, len(stored))
	}
	if len(stored) >= seeded {
		t.Fatalf("history = %d messages, want the %d-message session compressed", len(stored), seeded)
	}
	found := summarised(t, stored)
	if len(found) != 1 {
		t.Fatalf("summaries = %d, want exactly 1", len(found))
	}
	if found[0].Sender != router.Identity {
		t.Errorf("summary sender = %q, want %q: the summary describes the conversation, it is not something the user said",
			found[0].Sender, router.Identity)
	}
	if !strings.Contains(found[0].Text, "messages removed") {
		t.Errorf("summary = %q, want it to account for the messages it replaced", found[0].Text)
	}
	// The turn generated from the compressed session, not from the history it
	// was handed: the request carries the very summary the store now holds,
	// where a request built from the uncompressed history would carry the
	// model's own summary of a different span.
	var requestSummary string
	for _, m := range prepared.lastRequest.Messages {
		if strings.Contains(m.Content, DefaultCompressionConfig().SummaryMarker) {
			requestSummary = m.Content
		}
	}
	if requestSummary != found[0].Text {
		t.Errorf("the request's summary is not the session's summary: the turn was assembled from a different history")
	}

	// Once per crossing: compression put the session back under the budget, so
	// the turns that follow must not compress it again.
	for i := range 3 {
		if _, err := runner.Run(ctx, turnIn("chat-1", fmt.Sprintf("tg-next-%d", i), 2100+i), nil); err != nil {
			t.Fatalf("Run() after compression: %v", err)
		}
	}
	if store.replaces != 1 {
		t.Fatalf("replacements = %d after further turns, want 1: the session was already below the budget",
			store.replaces)
	}
	after, err := store.RecentMessages(ctx, sessionID, 1000)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if got := len(summarised(t, after)); got != 1 {
		t.Fatalf("summaries = %d after further turns, want exactly 1", got)
	}
}

// A long conversation that still fits its model's budget is left alone: the
// trigger answers to the budget, not to the passage of turns.
func TestTurnLeavesAnUnderBudgetSessionAlone(t *testing.T) {
	ctx := context.Background()
	store := newTriggerStore(t)
	router, sessionID := routerOn(t, store, "chat-1")
	models := &compressTriggerModelManager{
		models: []string{"local/large"}, activeModel: "local/large",
		details: map[string]ModelDetails{
			"local/large": {Ref: "local/large", ContextWindow: 200000, MaxOutputTokens: 4096},
		},
	}
	runner, _ := newTriggerRunner(t, store, models, router)

	seedLargeHistory(t, store, sessionID, 60)

	if _, err := runner.Run(ctx, turnIn("chat-1", "tg-1", 2000), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if store.replaces != 0 {
		t.Fatalf("replacements = %d, want 0: the session is inside the model's budget", store.replaces)
	}
	stored, err := store.RecentMessages(ctx, sessionID, 1000)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	// 60 bulk + 20 tail + this turn's inbound and reply, all untouched.
	if len(stored) != 82 {
		t.Fatalf("history = %d messages, want all 82", len(stored))
	}
	if found := summarised(t, stored); len(found) != 0 {
		t.Fatalf("summaries = %d, want none", len(found))
	}
}

// Without a resolved model context window there is no budget to compare the
// session against, so the trigger leaves it alone. Guessing -- the compressor's
// 128k compatibility window -- would rewrite a session nobody measured.
func TestTurnSkipsCompressionWhenTheModelBudgetIsUnknown(t *testing.T) {
	tests := []struct {
		name   string
		models ModelManager
	}{
		{
			name:   "the model manager publishes no details",
			models: &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		},
		{
			name: "the details carry no context window",
			models: &compressTriggerModelManager{
				models: []string{"test/model"}, activeModel: "test/model",
				details: map[string]ModelDetails{"test/model": {Ref: "test/model"}},
			},
		},
		{
			name: "there are no details for the active model",
			models: &compressTriggerModelManager{
				models: []string{"test/model"}, activeModel: "test/model",
				details: map[string]ModelDetails{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := newTriggerStore(t)
			router, sessionID := routerOn(t, store, "chat-1")
			runner, _ := newTriggerRunner(t, store, tc.models, router)

			// 70 messages of ~2000 tokens: over the 128k compatibility
			// window's 127750-token history budget, so a trigger that guessed
			// a window would compress this session.
			seedForCompress(t, store, sessionID, 70)

			if _, err := runner.Run(ctx, turnIn("chat-1", "tg-1", 2000), nil); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			if store.replaces != 0 {
				t.Fatalf("replacements = %d, want 0: the model's context window is unknown, so the budget is unknown",
					store.replaces)
			}
			stored, err := store.RecentMessages(ctx, sessionID, 1000)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if found := summarised(t, stored); len(found) != 0 {
				t.Fatalf("summaries = %d, want none", len(found))
			}
		})
	}
}
