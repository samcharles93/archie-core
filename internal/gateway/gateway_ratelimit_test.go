package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/ratelimit"
)

// senderInbound is inbound with SenderID set, the field the rate limiter
// keys on.
func senderInbound(senderID, text string) Inbound {
	in := inbound("chat-1", text)
	in.Message.SenderID = senderID
	return in
}

func TestRouteRateLimitBlocksOverBudget(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")
	r.Limiter = ratelimit.New(time.Minute, 1)

	reply, err := r.Route(context.Background(), senderInbound("u1", "hello"))
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "llm: hello" {
		t.Fatalf("first message reply = %q, want it to reach the LLM", reply)
	}

	reply, err = r.Route(context.Background(), senderInbound("u1", "hello again"))
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != rateLimitReply {
		t.Fatalf("second message reply = %q, want the rate-limit reply", reply)
	}
}

func TestRouteRateLimitIsPerSender(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")
	r.Limiter = ratelimit.New(time.Minute, 1)

	if _, err := r.Route(context.Background(), senderInbound("u1", "hi")); err != nil {
		t.Fatalf("Route: %v", err)
	}
	reply, err := r.Route(context.Background(), senderInbound("u2", "hi"))
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "llm: hi" {
		t.Fatalf("a different sender's message reply = %q, want it unaffected by u1's budget", reply)
	}
}

func TestRouteNoLimiterConfiguredNeverBlocks(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")

	for i := range 5 {
		reply, err := r.Route(context.Background(), senderInbound("u1", "hello"))
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if reply != "llm: hello" {
			t.Fatalf("call %d reply = %q, want it to reach the LLM with no limiter configured", i, reply)
		}
	}
}

// TestRouteEmptySenderIDNeverLimited pins the deliberate fail-open for
// channels (webhook routes with no configured path identity, e.g.) that
// have no stable per-sender identity to charge: there is no shared key to
// throttle everyone under, so they are left unlimited rather than blocked.
func TestRouteEmptySenderIDNeverLimited(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")
	r.Limiter = ratelimit.New(time.Minute, 1)

	for i := range 3 {
		reply, err := r.Route(context.Background(), inbound("chat-1", "hello"))
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if reply != "llm: hello" {
			t.Fatalf("call %d reply = %q, want it to reach the LLM with no SenderID", i, reply)
		}
	}
}

// TestRouteStreamLocalCommandChargesExactlyOnce pins RouteStream's local
// command fallthrough (into route, not Route) against double-charging the
// budget: a status query must consume the same one unit whether it goes
// through Route or RouteStream.
func TestRouteStreamLocalCommandChargesExactlyOnce(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
	r.Limiter = ratelimit.New(time.Minute, 2)
	r.LLMStream = func(context.Context, Inbound, TurnStream) (string, error) {
		return "streamed", nil
	}

	if _, err := r.RouteStream(context.Background(), senderInbound("u1", "/status"), DeltaFunc(func(string) {})); err != nil {
		t.Fatalf("RouteStream: %v", err)
	}

	// Budget was 2; the local command must have consumed exactly one, so
	// exactly one more free-text message should still get through.
	reply, err := r.RouteStream(context.Background(), senderInbound("u1", "hello"), DeltaFunc(func(string) {}))
	if err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if reply != "streamed" {
		t.Fatalf("second call reply = %q, want it to have budget left", reply)
	}

	reply, err = r.RouteStream(context.Background(), senderInbound("u1", "hello again"), DeltaFunc(func(string) {}))
	if err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if reply != rateLimitReply {
		t.Fatalf("third call reply = %q, want the budget exhausted after 2 charged calls", reply)
	}
}

func TestRouteStreamRateLimitBlocksStreamedReply(t *testing.T) {
	r := NewRouter(nil, nil, "test")
	r.Limiter = ratelimit.New(time.Minute, 1)
	streamCalls := 0
	r.LLMStream = func(context.Context, Inbound, TurnStream) (string, error) {
		streamCalls++
		return "streamed", nil
	}

	if _, err := r.RouteStream(context.Background(), senderInbound("u1", "hello"), DeltaFunc(func(string) {})); err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	reply, err := r.RouteStream(context.Background(), senderInbound("u1", "hello again"), DeltaFunc(func(string) {}))
	if err != nil {
		t.Fatalf("RouteStream: %v", err)
	}
	if reply != rateLimitReply {
		t.Fatalf("reply = %q, want the rate-limit reply", reply)
	}
	if streamCalls != 1 {
		t.Fatalf("LLMStream called %d times, want exactly 1 (the blocked call must not reach it)", streamCalls)
	}
}
