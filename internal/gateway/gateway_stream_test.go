package gateway

import (
	"context"
	"strings"
	"testing"
)

// The streamed chat adapter must degrade to the blocking responder when no
// streaming responder is set, so the chat contract's Stream can always be
// called without first checking for support. The degrade lives in
// streamTurn's contract now that every streamed entry is the adapter's.
func TestStreamFallsBackWhenNoStreamResponder(t *testing.T) {
	r := NewRouter(nil, func(context.Context, Inbound) (string, error) {
		return "blocking reply", nil
	}, "telegram")

	deltas, kind, text, err := drainStreamed(t, streamChat(r), inbound("", "hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if kind != "done" || text != "blocking reply" {
		t.Errorf("terminal event = (%s, %q), want (done, \"blocking reply\")", kind, text)
	}
	if len(deltas) != 0 {
		t.Errorf("expected no deltas without a stream responder, got %v", deltas)
	}
}

func TestStreamStreamsFreeText(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	r.LLMStream = func(_ context.Context, _ Inbound, stream TurnStream) (string, error) {
		for _, d := range []string{"a", "b", "c"} {
			stream.Delta(d)
		}
		return "abc", nil
	}

	deltas, kind, text, err := drainStreamed(t, streamChat(r), inbound("", "hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if kind != "done" || text != "abc" || strings.Join(deltas, "") != "abc" {
		t.Errorf("terminal=(%s,%q) deltas=%q, want (done,\"abc\") rendering \"abc\"", kind, text, deltas)
	}
}

// Local commands answer from local state in one step, so they must not be
// sent to the streaming responder.
func TestStreamDoesNotStreamLocalCommands(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	streamed := false
	r.LLMStream = func(context.Context, Inbound, TurnStream) (string, error) {
		streamed = true
		return "", nil
	}

	if _, _, _, err := drainStreamed(t, streamChat(r), inbound("", "/status")); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if streamed {
		t.Error("/status must not be routed to the streaming responder")
	}
}

func TestStreamRejectsUnknownCommandLocally(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	streamed := false
	r.LLMStream = func(context.Context, Inbound, TurnStream) (string, error) {
		streamed = true
		return "ok", nil
	}

	_, kind, text, err := drainStreamed(t, streamChat(r), inbound("", "/commands"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if streamed {
		t.Error("unknown /commands must not reach the streaming responder")
	}
	if kind != "done" || !strings.Contains(text, "/help") {
		t.Errorf("terminal = (%s, %q), want the local unknown-command reply with /help guidance", kind, text)
	}
}
