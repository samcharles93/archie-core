package gateway

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/tools"
)

// fakeInteractive is a test-double channel adapter. It reports exactly the
// capabilities it was configured with and records the last request each
// requester method received, so a test can prove which path the gateway took.
type fakeInteractive struct {
	clarify bool
	picker  bool

	answer   string
	choice   messaging.InteractiveChoice
	sendErr  error
	lastClar messaging.ClarifyRequest
	lastPick messaging.PickerRequest
}

func (f *fakeInteractive) Capabilities() messaging.AdapterCapabilities {
	return messaging.AdapterCapabilities{Clarify: f.clarify, Picker: f.picker}
}

func (f *fakeInteractive) RequestClarification(_ context.Context, req messaging.ClarifyRequest) (string, error) {
	f.lastClar = req
	return f.answer, f.sendErr
}

func (f *fakeInteractive) RequestChoice(_ context.Context, req messaging.PickerRequest) (messaging.InteractiveChoice, error) {
	f.lastPick = req
	return f.choice, f.sendErr
}

func askUserEntry(t *testing.T, entries []tools.ToolEntry) tools.ToolEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == askUserToolName {
			return entry
		}
	}
	t.Fatalf("InteractiveTools did not register %q: %+v", askUserToolName, entries)
	return tools.ToolEntry{}
}

func TestInteractiveToolsAbsentWithoutCarrier(t *testing.T) {
	if entries := InteractiveTools(context.Background()); len(entries) != 0 {
		t.Fatalf("InteractiveTools without a carrier = %+v, want none", entries)
	}
}

func TestInteractiveToolsRegisteredWithCarrier(t *testing.T) {
	sender := &fakeInteractive{clarify: true, picker: true}
	ctx := WithInteractive(context.Background(), InteractiveOf(sender))

	entry := askUserEntry(t, InteractiveTools(ctx))
	if entry.Handler == nil {
		t.Fatal("ask_user has no handler")
	}
}

func TestAskUserUsesPickerWhenChannelCanPick(t *testing.T) {
	sender := &fakeInteractive{
		clarify: true,
		picker:  true,
		choice:  messaging.InteractiveChoice{ID: "release", Label: "release"},
	}
	ctx := WithInteractive(context.Background(), InteractiveOf(sender))
	entry := askUserEntry(t, InteractiveTools(ctx))

	out, err := entry.Handler(ctx, map[string]any{
		"question": "Which branch?",
		"options":  []any{"main", "release"},
	})
	if err != nil {
		t.Fatalf("ask_user error = %v", err)
	}
	if sender.lastPick.Prompt != "Which branch?" {
		t.Errorf("picker prompt = %q, want %q", sender.lastPick.Prompt, "Which branch?")
	}
	if len(sender.lastPick.Options) != 2 {
		t.Fatalf("picker options = %+v, want 2", sender.lastPick.Options)
	}
	got, ok := out.(map[string]any)
	if !ok || got["value"] != "release" {
		t.Fatalf("ask_user output = %#v, want value release", out)
	}
	if sender.lastClar.Question != "" {
		t.Errorf("picker-capable channel also received a clarify request: %q", sender.lastClar.Question)
	}
}

// TestAskUserFallsBackToTextWhenChannelCannotPick is the acceptance test for
// step 4: a channel that reports Clarify but not Picker must still carry a
// choice, rendered as text and matched from the human's typed reply rather
// than failing or dropping the question.
func TestAskUserFallsBackToTextWhenChannelCannotPick(t *testing.T) {
	sender := &fakeInteractive{clarify: true, answer: "2"}
	ctx := WithInteractive(context.Background(), InteractiveOf(sender))
	entry := askUserEntry(t, InteractiveTools(ctx))

	out, err := entry.Handler(ctx, map[string]any{
		"question": "Which branch?",
		"options":  []any{"main", "release"},
	})
	if err != nil {
		t.Fatalf("ask_user error = %v", err)
	}
	want := FormatPickerText(PickerRequest{
		Prompt: "Which branch?",
		Options: []InteractiveChoice{
			{ID: "main", Label: "main"},
			{ID: "release", Label: "release"},
		},
	})
	if sender.lastClar.Question != want {
		t.Errorf("text fallback prompt =\n%q\nwant\n%q", sender.lastClar.Question, want)
	}
	got, ok := out.(map[string]any)
	if !ok || got["value"] != "release" {
		t.Fatalf("ask_user output = %#v, want value release", out)
	}
}

// TestTurnRegistersAskUserOnlyWithCarrier proves the contract is reachable
// from a real turn: prepareTurn offers the question tool to the model only
// when the turn's context carries a channel adapter that can answer it.
func TestTurnRegistersAskUserOnlyWithCarrier(t *testing.T) {
	newRunner := func() (*TurnRunner, *turnTestModel) {
		store := NewSessionStoreMemory()
		router := NewRouter(nil, nil, "telegram")
		router.Identity = "archie"
		router.InitSessions(store)
		model := &turnTestModel{prepared: &turnTestPreparedModel{reply: "ok"}}
		return NewTurnRunner(TurnRunnerConfig{
			Router:   router,
			Sessions: store,
			Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
			Personas: NewPersonaRegistry(DefaultPersonas()),
			Model:    model,
			BotUser:  "archie",
			Channel:  "telegram",
			Log:      slog.New(slog.DiscardHandler),
		}), model
	}
	run := func(ctx context.Context, runner *TurnRunner) {
		in := Inbound{Message: messaging.Message{
			SourceID:       "source",
			ConversationID: messaging.ConversationID{ChannelID: "chat-1"},
			Sender:         "user",
			Role:           messaging.RoleUser,
			Text:           "hello",
		}}
		if _, err := runner.Run(ctx, in, nil); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	hasAskUser := func(extra []tools.ToolEntry) bool {
		for _, entry := range extra {
			if entry.Name == askUserToolName {
				return true
			}
		}
		return false
	}

	runner, model := newRunner()
	run(context.Background(), runner)
	if hasAskUser(model.gotExtra) {
		t.Error("ask_user was registered for a turn whose channel cannot ask a question")
	}

	runner, model = newRunner()
	ctx := WithInteractive(context.Background(), InteractiveOf(&fakeInteractive{clarify: true, picker: true}))
	run(ctx, runner)
	if !hasAskUser(model.gotExtra) {
		t.Fatalf("ask_user was not registered with a carrier: %+v", model.gotExtra)
	}
}
