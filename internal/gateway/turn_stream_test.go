package gateway

import (
	"context"
	"log/slog"
	"testing"
)

// recordingStream captures everything a turn reports, in arrival order, so a
// test can assert that text and tool activity stay interleaved as produced.
type recordingStream struct {
	events []string
}

func (s *recordingStream) Delta(text string) {
	s.events = append(s.events, "text:"+text)
}

func (s *recordingStream) ToolCall(event ToolCallEvent) {
	s.events = append(s.events, "tool:"+event.Name+":"+event.Summary())
}

func newStreamTestRunner(t *testing.T, prepared *turnTestPreparedModel) *TurnRunner {
	t.Helper()
	store := NewSessionStoreMemory()
	router := NewRouter(nil, nil, "telegram")
	router.Identity = "archie"
	router.InitSessions(store)
	return NewTurnRunner(TurnRunnerConfig{
		Router:   router,
		Sessions: store,
		Models:   &fakeModelManager{models: []string{"test/model"}, activeModel: "test/model"},
		Personas: NewPersonaRegistry(DefaultPersonas()),
		Model:    &turnTestModel{prepared: prepared},
		BotUser:  "archie",
		Channel:  "telegram",
		Log:      slog.New(slog.DiscardHandler),
	})
}

// A channel that renders tool activity has to receive it in the order the
// model produced it, not as a batch after the answer.
func TestTurnRunnerForwardsToolCallsInOrderWithText(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "done"}
	prepared.generateStream = func(stream TurnStream) (string, error) {
		stream.Delta("looking")
		stream.ToolCall(ToolCallEvent{Name: "shell", Output: "exit 0"})
		stream.Delta(" and answering")
		return "looking and answering", nil
	}
	runner := newStreamTestRunner(t, prepared)

	sink := &recordingStream{}
	reply, err := runner.Run(context.Background(), Message{
		From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello",
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reply != "looking and answering" {
		t.Fatalf("reply = %q", reply)
	}

	want := []string{"text:looking", "tool:shell:exit 0", "text: and answering"}
	if len(sink.events) != len(want) {
		t.Fatalf("stream events = %v, want %v", sink.events, want)
	}
	for i := range want {
		if sink.events[i] != want[i] {
			t.Fatalf("stream events = %v, want %v", sink.events, want)
		}
	}
}

// A redelivered message replays its stored reply without calling the model,
// so the channel gets the whole answer as one fragment and no tool activity.
func TestTurnRunnerReplayStreamsStoredReplyOnce(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "the answer"}
	runner := newStreamTestRunner(t, prepared)
	msg := Message{From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello"}

	if _, err := runner.Run(context.Background(), msg, DeltaFunc(nil)); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	sink := &recordingStream{}
	reply, err := runner.Run(context.Background(), msg, sink)
	if err != nil {
		t.Fatalf("replay Run() error = %v", err)
	}
	if reply != "the answer" {
		t.Fatalf("replayed reply = %q", reply)
	}
	if len(sink.events) != 1 || sink.events[0] != "text:the answer" {
		t.Fatalf("replay stream events = %v, want one full-text delta", sink.events)
	}
	if prepared.generateN != 1 {
		t.Fatalf("Generate calls = %d, want 1  --  a replay must not call the model", prepared.generateN)
	}
}

// A completed-duplicate replay (NATS redelivery, restart RecoverTurns
// redelivery) must narrate the same tool activity the original turn did, in
// the same order, ahead of the answer -- otherwise the duplicate disagrees
// with the original about what ran (archie-core-ippu).
func TestTurnRunnerReplayCompletedDuplicateReplaysToolCalls(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "done"}
	prepared.generateStream = func(stream TurnStream) (string, error) {
		stream.ToolCall(ToolCallEvent{Name: "shell", Output: "exit 0"})
		stream.Delta("done")
		return "done", nil
	}
	runner := newStreamTestRunner(t, prepared)
	msg := Message{From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello"}

	if _, err := runner.Run(context.Background(), msg, &recordingStream{}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	sink := &recordingStream{}
	reply, err := runner.Run(context.Background(), msg, sink)
	if err != nil {
		t.Fatalf("replay Run() error = %v", err)
	}
	if reply != "done" {
		t.Fatalf("replayed reply = %q", reply)
	}
	want := []string{"tool:shell:exit 0", "text:done"}
	if len(sink.events) != len(want) {
		t.Fatalf("replay stream events = %v, want %v", sink.events, want)
	}
	for i := range want {
		if sink.events[i] != want[i] {
			t.Fatalf("replay stream events = %v, want %v", sink.events, want)
		}
	}
	if prepared.generateN != 1 {
		t.Fatalf("Generate calls = %d, want 1  --  a replay must not call the model", prepared.generateN)
	}

	turn, ok, err := runner.Ledger.GetTurn(context.Background(), CanonicalTurnID("chat-1", "source-1"))
	if err != nil || !ok {
		t.Fatalf("GetTurn() = %#v, %v, %v; want persisted turn", turn, ok, err)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "shell" {
		t.Fatalf("persisted turn tool calls = %#v, want one recorded shell call", turn.ToolCalls)
	}
}

// A turn's recorded tool activity is captured even when the original caller
// did not stream (Router.LLM, a nil sink): a later duplicate delivered to a
// streaming caller must still be able to replay it.
func TestTurnRunnerRecordsToolCallsEvenWithoutOriginalStream(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "done"}
	prepared.generateStream = func(stream TurnStream) (string, error) {
		stream.ToolCall(ToolCallEvent{Name: "grep", Output: "3 matches"})
		return "done", nil
	}
	runner := newStreamTestRunner(t, prepared)
	msg := Message{From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello"}

	if _, err := runner.Run(context.Background(), msg, nil); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	sink := &recordingStream{}
	if _, err := runner.Run(context.Background(), msg, sink); err != nil {
		t.Fatalf("replay Run() error = %v", err)
	}
	want := []string{"tool:grep:3 matches", "text:done"}
	if len(sink.events) != len(want) {
		t.Fatalf("replay stream events = %v, want %v", sink.events, want)
	}
	for i := range want {
		if sink.events[i] != want[i] {
			t.Fatalf("replay stream events = %v, want %v", sink.events, want)
		}
	}
}

// A nil sink means "not streaming": the turn still runs and returns its reply.
func TestTurnRunnerAcceptsNoStream(t *testing.T) {
	prepared := &turnTestPreparedModel{reply: "quiet answer"}
	runner := newStreamTestRunner(t, prepared)

	reply, err := runner.Run(context.Background(), Message{
		From: "user", ChannelID: "chat-1", SourceID: "source-1", Text: "hello",
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reply != "quiet answer" {
		t.Fatalf("reply = %q", reply)
	}
}
