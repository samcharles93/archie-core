package gateway

import (
	"context"
	"log/slog"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestTurnRunnerGivesInboundMediaToTheModelRequest pins the handoff point
// between the adapter and the model seam: media a channel attached to an
// inbound message must reach the model request, or the channel's work is
// lost and the turn answers on caption text alone.
func TestTurnRunnerGivesInboundMediaToTheModelRequest(t *testing.T) {
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
		Operator: "Sam",
		Log:      slog.New(slog.DiscardHandler),
	})

	media := []messaging.MediaAttachment{{Type: "image", MIMEType: "image/png", Data: []byte("png-bytes")}}
	_, err := runner.Run(context.Background(), Inbound{
		Message: messaging.Message{
			SourceID:       "src-media",
			ConversationID: messaging.ConversationID{ChannelID: "chat-media"},
			Sender:         "user",
			Role:           messaging.RoleUser,
			Text:           "look at this",
		},
		Media: media,
	}, DeltaFunc(func(string) {}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(prepared.lastRequest.Media) != 1 {
		t.Fatalf("model request media = %#v, want the one inbound attachment", prepared.lastRequest.Media)
	}
	if !reflect.DeepEqual(prepared.lastRequest.Media, media) {
		t.Errorf("model request media = %#v, want %#v", prepared.lastRequest.Media, media)
	}
}
