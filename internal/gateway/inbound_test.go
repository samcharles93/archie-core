package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// inbound is what a channel hands the Router: a user message in channelID's
// chat. Tests that also need a source ID, thread or timestamp set those on
// the returned value's Message.
func inbound(channelID, text string) Inbound {
	return Inbound{Message: messaging.Message{
		ConversationID: messaging.ConversationID{ChannelID: channelID},
		Role:           messaging.RoleUser,
		Text:           text,
	}}
}

// inboundFrom is inbound with the channel-native sender attribution set.
func inboundFrom(channelID, sender, text string) Inbound {
	in := inbound(channelID, text)
	in.Message.Sender = sender
	return in
}

// TestPageReachesThePromptButNotTheRecord pins the reason Inbound exists.
// Page is the dashboard route the operator is looking at: it belongs in the
// system prompt and nowhere else, and the record the turn persists is the
// canonical messaging.Message, which has no field that could carry it.
func TestPageReachesThePromptButNotTheRecord(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "you are on the tasks page"}
	store := NewSessionStoreMemory()
	t.Cleanup(func() { _ = store.Close() })
	router := NewRouter(nil, nil, "web")
	router.Identity = "archie"
	router.InitSessions(store)
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Model:    &turnTestModel{prepared: prepared},
		BotUser:  "archie",
		Channel:  "web",
	})

	in := inboundFrom("browser-1", "web", "where am I?")
	in.Message.SourceID = "web-1"
	in.Page = "/tasks"
	if _, err := runner.Run(t.Context(), in, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	system := prepared.lastRequest.Messages[0]
	if system.Role != "system" || !strings.Contains(system.Content, "/tasks") {
		t.Fatalf("system prompt = %q, want the operator's page in it", system.Content)
	}

	history, err := store.RecentMessages(t.Context(), "browser-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %#v, want the user message and its reply", history)
	}
	for _, stored := range history {
		if strings.Contains(stored.Text, "/tasks") || stored.Sender == "/tasks" {
			t.Fatalf("page leaked into the persisted record: %+v", stored)
		}
	}
}

// TestChannelInboundRoundTripsWithItsRole pins the other half of the flip:
// the role a channel sets at construction is the role that comes back out
// of the store, on both sides of the exchange. Role used to be derived at
// the store boundary by comparing the sender against the bot identity, so a
// user whose channel name matched the bot's was filed as the assistant
// however the message had arrived.
func TestChannelInboundRoundTripsWithItsRole(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "on it"}
	store := NewSessionStoreMemory()
	t.Cleanup(func() { _ = store.Close() })
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Model:    &turnTestModel{prepared: prepared},
		BotUser:  "archie",
		Channel:  "telegram",
	})

	in := inboundFrom("chat-1", "archie", "deploy it")
	in.Message.SourceID = "tg-7"
	in.Message.At = time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	if _, err := runner.Run(t.Context(), in, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	history, err := store.RecentMessages(t.Context(), "chat-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %#v, want the user message and its reply", history)
	}
	user, reply := history[0], history[1]
	if user.Role != messaging.RoleUser || user.Text != "deploy it" || user.Sender != "archie" {
		t.Fatalf("stored user message = %+v, want the role the channel set", user)
	}
	if user.SourceID != "tg-7" || user.ID == "" {
		t.Fatalf("stored user message lost its correlation: %+v", user)
	}
	if reply.Role != messaging.RoleAssistant || reply.Sender != "archie" {
		t.Fatalf("stored reply = %+v, want an assistant message from the bot", reply)
	}
}
