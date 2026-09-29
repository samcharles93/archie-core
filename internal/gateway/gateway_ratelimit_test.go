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
	// The batcher is off: nothing here is about coalescing, and its window
	// must not sit between a dispatch and this test's assertions.
	r.Batches = nil

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
	// The batcher is off: these dispatches must be immediate, and each new
	// sender would otherwise wait out its own quiet window.
	r.Batches = nil

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
	// The batcher is off: each allowed dispatch must be immediate here.
	r.Batches = nil

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
// channels that have no stable per-sender identity to charge and no key of
// their own to budget against: there is no shared key to throttle everyone
// under, so they are left unlimited rather than blocked. A channel that has
// a stable source but no per-person identity (a webhook route) sets
// Inbound.BudgetKey instead -- see TestRouteBudgetKeyLimitsWithoutSenderID.
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

// TestRouteBudgetKeyLimitsWithoutSenderID pins the split between a message's
// per-person identity and the key it is rate limited against. A webhook has
// no person to identify, so SenderID stays empty, but its route is still a
// bounded, operator-controlled source that must keep its budget -- otherwise
// clearing SenderID would silently exempt every webhook route from the
// limiter config.RateLimitConfig documents.
func TestRouteBudgetKeyLimitsWithoutSenderID(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")
	r.Limiter = ratelimit.New(time.Minute, 1)

	budgeted := func(budgetKey, text string) Inbound {
		in := inbound("chat-1", text)
		in.BudgetKey = budgetKey
		return in
	}

	reply, err := r.Route(context.Background(), budgeted("/hook", "hello"))
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "llm: hello" {
		t.Fatalf("first message reply = %q, want it to reach the LLM", reply)
	}

	reply, err = r.Route(context.Background(), budgeted("/hook", "hello again"))
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != rateLimitReply {
		t.Fatalf("second message reply = %q, want the rate-limit reply", reply)
	}

	// A different route is a different source, so it is unaffected by the
	// first route's spent budget -- the same per-source property
	// TestRouteRateLimitIsPerSender pins for real senders.
	reply, err = r.Route(context.Background(), budgeted("/other", "hello"))
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "llm: hello" {
		t.Fatalf("other route reply = %q, want it unaffected by /hook's budget", reply)
	}
}

// TestStreamLocalCommandChargesExactlyOnce pins the streamed adapter's local
// command fallthrough (into route, not the streaming responder) against
// double-charging the budget: a status query must consume the same one unit
// whether it goes through Route or the adapter's Stream.
func TestStreamLocalCommandChargesExactlyOnce(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
	r.Limiter = ratelimit.New(time.Minute, 2)
	// The batcher is off: the charge being pinned is per message, not per batch.
	r.Batches = nil
	r.LLMStream = func(context.Context, Inbound, TurnStream) (string, error) {
		return "streamed", nil
	}
	if _, _, _, err := drainStreamed(t, streamChat(r), senderInbound("u1", "/status")); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Budget was 2; the local command must have consumed exactly one, so
	// exactly one more free-text message should still get through.
	_, kind, text, err := drainStreamed(t, streamChat(r), senderInbound("u1", "hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if kind != "done" || text != "streamed" {
		t.Fatalf("second call terminal = (%s, %q), want it to have budget left", kind, text)
	}

	_, kind, text, err = drainStreamed(t, streamChat(r), senderInbound("u1", "hello again"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if kind != "done" || text != rateLimitReply {
		t.Fatalf("third call terminal = (%s, %q), want the budget exhausted after 2 charged calls", kind, text)
	}
}

func TestStreamRateLimitBlocksStreamedReply(t *testing.T) {
	r := NewRouter(nil, nil, "test")
	r.Limiter = ratelimit.New(time.Minute, 1)
	// The batcher is off: the blocked call must not wait on a batch window.
	r.Batches = nil
	streamCalls := 0
	r.LLMStream = func(context.Context, Inbound, TurnStream) (string, error) {
		streamCalls++
		return "streamed", nil
	}

	if _, _, _, err := drainStreamed(t, streamChat(r), senderInbound("u1", "hello")); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, kind, text, err := drainStreamed(t, streamChat(r), senderInbound("u1", "hello again"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if kind != "done" || text != rateLimitReply {
		t.Fatalf("terminal = (%s, %q), want the rate-limit reply", kind, text)
	}
	if streamCalls != 1 {
		t.Fatalf("LLMStream called %d times, want exactly 1 (the blocked call must not reach it)", streamCalls)
	}
}
