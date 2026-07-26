package gateway

import (
	"context"
	"strings"
	"testing"
)

// RouteStream must degrade to Route when no streaming responder is set, so
// adapters can always call it without first checking for support.
func TestRouteStreamFallsBackWhenNoStreamResponder(t *testing.T) {
	r := NewRouter(nil, func(context.Context, Message) (string, error) {
		return "blocking reply", nil
	}, "telegram")

	var deltas []string
	got, err := r.RouteStream(context.Background(), Message{Text: "hello"}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if got != "blocking reply" {
		t.Errorf("reply = %q, want %q", got, "blocking reply")
	}
	if len(deltas) != 0 {
		t.Errorf("expected no deltas without a stream responder, got %v", deltas)
	}
}

func TestRouteStreamStreamsFreeText(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	r.LLMStream = func(_ context.Context, _ Message, onDelta func(string)) (string, error) {
		for _, d := range []string{"a", "b", "c"} {
			onDelta(d)
		}
		return "abc", nil
	}

	var sb strings.Builder
	got, err := r.RouteStream(context.Background(), Message{Text: "hello"}, func(d string) {
		sb.WriteString(d)
	})
	if err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if got != "abc" || sb.String() != "abc" {
		t.Errorf("reply=%q deltas=%q, want both %q", got, sb.String(), "abc")
	}
}

// Local commands answer from local state in one step, so they must not be
// sent to the streaming responder.
func TestRouteStreamDoesNotStreamLocalCommands(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	streamed := false
	r.LLMStream = func(context.Context, Message, func(string)) (string, error) {
		streamed = true
		return "", nil
	}

	if _, err := r.RouteStream(context.Background(), Message{Text: "/status"}, func(string) {}); err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if streamed {
		t.Error("/status must not be routed to the streaming responder")
	}
}

// An unrecognised slash command is handed to the LLM by Route, so it should
// stream like any other free text.
func TestRouteStreamStreamsUnknownCommand(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	streamed := false
	r.LLMStream = func(context.Context, Message, func(string)) (string, error) {
		streamed = true
		return "ok", nil
	}

	if _, err := r.RouteStream(context.Background(), Message{Text: "/commands"}, func(string) {}); err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if !streamed {
		t.Error("unknown /commands should stream, since Route sends it to the LLM")
	}
}
