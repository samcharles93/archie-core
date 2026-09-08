package webui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

// newTestPump builds a pump over the remote State Store contract, which is
// the composition the UI process runs: the pump's only view of an event is
// the one that came back over the wire.
func newTestPump(t *testing.T, srv *Server) *eventPump {
	t.Helper()
	return srv.newEventPump()
}

// Priming is what keeps the pump from replaying the whole events table into
// every connected browser on startup. History is a client's own catch-up
// (sseStream.catchUp), not the pump's job.
func TestEventPumpPrimesPastHistoryThenDeliversNewEvents(t *testing.T) {
	srv := newRemoteTestServer(t)
	for _, detail := range []string{"first", "second"} {
		if _, err := srv.Store.InsertEvent(t.Context(), events.Event{Kind: "task_created", Detail: detail}); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}

	pump := newTestPump(t, srv)
	if err := pump.prime(t.Context()); err != nil {
		t.Fatalf("prime: %v", err)
	}

	delivered, err := pump.deliver(t.Context())
	if err != nil {
		t.Fatalf("deliver after prime: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("pump delivered %d pre-existing events; priming must advance past history", delivered)
	}

	id, err := srv.Store.InsertEvent(t.Context(), events.Event{Kind: "task_started", Detail: "after the pump"})
	if err != nil {
		t.Fatalf("insert live event: %v", err)
	}
	delivered, err = pump.deliver(t.Context())
	if err != nil {
		t.Fatalf("deliver live event: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("pump delivered %d events, want the 1 inserted after priming", delivered)
	}
	if pump.watermark != id {
		t.Fatalf("watermark = %d, want %d: the pump must not refetch a delivered event", pump.watermark, id)
	}
}

// A store failure must not kill the pump: the next tick retries from the
// same watermark, so a State Store restart costs latency, not the feed.
func TestEventPumpKeepsItsWatermarkAcrossAStoreFailure(t *testing.T) {
	srv := newRemoteTestServer(t)
	pump := newTestPump(t, srv)
	if err := pump.prime(t.Context()); err != nil {
		t.Fatalf("prime: %v", err)
	}
	before := pump.watermark

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := pump.deliver(cancelled); err == nil {
		t.Fatal("deliver against a cancelled context returned no error")
	}
	if pump.watermark != before {
		t.Fatalf("watermark moved to %d on a failed fetch, want %d", pump.watermark, before)
	}
}

// The bead's headline claim: an event written to the State Store by another
// process reaches a browser hanging on /events, with no in-process bus.
func TestEventPumpReachesALiveSSEClient(t *testing.T) {
	srv := newRemoteTestServer(t)
	pump := newTestPump(t, srv)
	if err := pump.prime(t.Context()); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// handleSSE writes no response headers until its first send, so the
	// client blocks on Do() until something is on the wire. Seed one event
	// for catch-up to flush, then prove the pump delivers what comes after.
	if _, err := srv.Store.InsertEvent(t.Context(), events.Event{Kind: "task_created", Detail: "flushes the headers"}); err != nil {
		t.Fatalf("seed catch-up event: %v", err)
	}
	ts := newSSETestServer(t, srv)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	lines := openSSEStream(t, ctx, ts)

	go pump.run(ctx, 5*time.Millisecond)

	if _, err := srv.Store.InsertEvent(t.Context(), events.Event{Kind: "task_started", Detail: "over the wire"}); err != nil {
		t.Fatalf("insert live event: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-lines:
			if strings.Contains(line, "over the wire") {
				return
			}
		case <-deadline:
			t.Fatal("live event never reached the SSE client; the pump is the only delivery path in the UI process")
		}
	}
}

// newSSETestServer serves the dashboard's real route set, so /events goes
// through requireToken and the mux exactly as a browser reaches it.
func newSSETestServer(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// openSSEStream connects to /events and relays each response line on a
// channel. The request context ends the handler, which is what lets the
// test server close.
func openSSEStream(t *testing.T, ctx context.Context, ts *httptest.Server) <-chan string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatalf("sse request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	// Closed at cleanup rather than by the reader goroutine: the body is the
	// live stream and must outlive this function, but it still has exactly
	// one owner and one close.
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events = %d, want 200", resp.StatusCode)
	}
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines
}
