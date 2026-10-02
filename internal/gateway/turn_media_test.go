package gateway

import (
	"context"
	"errors"
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

// fakeTranscriber records the audio it is handed and returns a fixed
// transcript, so a test can prove the model-owning turn runner transcribed
// the bytes a channel frontend carried across the boundary.
type fakeTranscriber struct {
	text  string
	err   error
	calls int
	audio []byte
}

func (f *fakeTranscriber) Transcribe(_ context.Context, audio []byte) (string, error) {
	f.calls++
	f.audio = append([]byte(nil), audio...)
	return f.text, f.err
}

// TestTurnRunnerTranscribesSpeechOnTheModelOwningSide drives the behaviour the
// boundary move exists for: a voice attachment's bytes reach the process that
// owns the model and provider credentials, which turns them into the turn's
// text before the inbound message is recorded. The stored history -- the only
// thing a later turn can read, since the audio is gone -- must carry the
// transcript, not the frontend's "[voice message]" note.
func TestTurnRunnerTranscribesSpeechOnTheModelOwningSide(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)

	prepared := &turnTestPreparedModel{reply: "answer"}
	transcriber := &fakeTranscriber{text: "turn on the lights"}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:      router,
		Sessions:    store,
		Models:      &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas:    NewPersonaRegistry(DefaultPersonas()),
		Model:       &turnTestModel{prepared: prepared},
		Transcriber: transcriber,
		BotUser:     "archie",
		Channel:     "telegram",
		Operator:    "Sam",
		Log:         slog.New(slog.DiscardHandler),
	})

	voiceBytes := []byte("ogg-voice-bytes")
	_, err := runner.Run(context.Background(), Inbound{
		Message: messaging.Message{
			SourceID:       "src-voice",
			ConversationID: messaging.ConversationID{ChannelID: "chat-voice"},
			Sender:         "user",
			Role:           messaging.RoleUser,
			Text:           "[voice message]",
		},
		Media: []messaging.MediaAttachment{{Type: messaging.MediaTypeVoice, MIMEType: "audio/ogg", Data: voiceBytes}},
	}, DeltaFunc(func(string) {}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if transcriber.calls != 1 {
		t.Fatalf("Transcribe calls = %d, want 1", transcriber.calls)
	}
	if string(transcriber.audio) != string(voiceBytes) {
		t.Errorf("Transcribe received %q, want the attachment bytes carried across the boundary", transcriber.audio)
	}
	history, err := store.RecentMessages(context.Background(), "chat-voice", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) == 0 {
		t.Fatal("no stored history; the inbound turn was not recorded")
	}
	want := "[voice transcription]\nturn on the lights"
	if history[0].Text != want {
		t.Errorf("stored inbound text = %q, want %q", history[0].Text, want)
	}
}

// TestTurnRunnerKeepsTheNoteWhenTranscriptionDegrades pins the required
// degradation: a nil Transcriber, a failed call, or an empty transcript leaves
// the frontend's media note in place rather than failing the turn.
func TestTurnRunnerKeepsTheNoteWhenTranscriptionDegrades(t *testing.T) {
	cases := []struct {
		name        string
		transcriber messaging.Transcriber
	}{
		{name: "no transcriber configured"},
		{name: "transcriber failed", transcriber: &fakeTranscriber{err: errors.New("provider down")}},
		{name: "empty transcript", transcriber: &fakeTranscriber{text: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewSessionStoreMemory()
			router := NewRouter(nil, nil, "telegram")
			router.Identity = "archie"
			router.InitSessions(store)

			runner := NewTurnRunner(TurnRunnerConfig{
				Router:      router,
				Sessions:    store,
				Models:      &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
				Personas:    NewPersonaRegistry(DefaultPersonas()),
				Model:       &turnTestModel{prepared: &turnTestPreparedModel{reply: "answer"}},
				Transcriber: tc.transcriber,
				BotUser:     "archie",
				Channel:     "telegram",
				Operator:    "Sam",
				Log:         slog.New(slog.DiscardHandler),
			})

			_, err := runner.Run(context.Background(), Inbound{
				Message: messaging.Message{
					SourceID:       "src-voice-" + tc.name,
					ConversationID: messaging.ConversationID{ChannelID: "chat-voice-degrade"},
					Sender:         "user",
					Role:           messaging.RoleUser,
					Text:           "[voice message]",
				},
				Media: []messaging.MediaAttachment{{Type: messaging.MediaTypeVoice, MIMEType: "audio/ogg", Data: []byte("ogg-voice-bytes")}},
			}, DeltaFunc(func(string) {}))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			history, err := store.RecentMessages(context.Background(), "chat-voice-degrade", 10)
			if err != nil {
				t.Fatalf("RecentMessages() error = %v", err)
			}
			if len(history) == 0 || history[0].Text != "[voice message]" {
				t.Fatalf("stored inbound text = %#v, want the media note preserved", history)
			}
		})
	}
}

// TestTurnRunnerDoesNotTranscribePlainAudio keeps the capability scoped to
// speech: a forwarded music file reaches the model as an audio attachment, not
// a transcription call.
func TestTurnRunnerDoesNotTranscribePlainAudio(t *testing.T) {
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)

	transcriber := &fakeTranscriber{text: "should not be used"}
	runner := NewTurnRunner(TurnRunnerConfig{
		Router:      router,
		Sessions:    store,
		Models:      &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas:    NewPersonaRegistry(DefaultPersonas()),
		Model:       &turnTestModel{prepared: &turnTestPreparedModel{reply: "answer"}},
		Transcriber: transcriber,
		BotUser:     "archie",
		Channel:     "telegram",
		Operator:    "Sam",
		Log:         slog.New(slog.DiscardHandler),
	})

	_, err := runner.Run(context.Background(), Inbound{
		Message: messaging.Message{
			SourceID:       "src-audio",
			ConversationID: messaging.ConversationID{ChannelID: "chat-audio"},
			Sender:         "user",
			Role:           messaging.RoleUser,
			Text:           "[audio]",
		},
		Media: []messaging.MediaAttachment{{Type: messaging.MediaTypeAudio, MIMEType: "audio/mpeg", Data: []byte("mp3-bytes")}},
	}, DeltaFunc(func(string) {}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if transcriber.calls != 0 {
		t.Errorf("Transcribe calls = %d, want 0 for non-speech audio", transcriber.calls)
	}
	history, err := store.RecentMessages(context.Background(), "chat-audio", 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(history) == 0 || history[0].Text != "[audio]" {
		t.Fatalf("stored inbound text = %#v, want the plain audio note", history)
	}
}
