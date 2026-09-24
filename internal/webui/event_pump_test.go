package webui

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
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
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, detail := range []string{"first", "second"} {
		if _, err := srv.Store.InsertEvent(t.Context(), events.Event{Kind: "task_created", Detail: detail, At: t0}); err != nil {
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

	t1 := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	id, err := srv.Store.InsertEvent(t.Context(), events.Event{Kind: "task_started", Detail: "after the pump", At: t1})
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
	if want := storecontract.EventCursor(t1, id); pump.watermark != want {
		t.Fatalf("watermark = %q, want %q: the pump must not refetch a delivered event", pump.watermark, want)
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
		t.Fatalf("watermark moved to %q on a failed fetch, want %q", pump.watermark, before)
	}
}

// The bead's headline claim: an event written to the State Store by another
// process reaches a browser hanging on /api/stream, with no in-process bus.
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

// newSSETestServer serves the dashboard's real route set, so /api/stream goes
// through requireToken and the mux exactly as a browser reaches it.
func newSSETestServer(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// openSSEStream connects to /api/stream and relays each response line on a
// channel. The request context ends the handler, which is what lets the
// test server close.
func openSSEStream(t *testing.T, ctx context.Context, ts *httptest.Server) <-chan string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/stream", nil)
	if err != nil {
		t.Fatalf("sse request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/stream: %v", err)
	}
	// Closed at cleanup rather than by the reader goroutine: the body is the
	// live stream and must outlive this function, but it still has exactly
	// one owner and one close.
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/stream = %d, want 200", resp.StatusCode)
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
		if err := scanner.Err(); err != nil {
			return
		}
	}()
	return lines
}

// flakyEventReader fails its first failures calls, then answers an empty
// table. It stands in for the State Store during the window where the UI
// process is up and the store has not bound its listener yet.
type flakyEventReader struct {
	failures int
	calls    int
}

func (f *flakyEventReader) EventsSince(context.Context, string, int) ([]events.Event, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, errors.New("connection refused")
	}
	return nil, nil
}

// archie-ui binds before archie-state-store is guaranteed to be listening, so
// the first prime is routinely refused. Returning on that error left the
// activity feed history-only until the process was restarted by hand.
func TestPumpPriming(t *testing.T) {
	forever := 1 << 30

	tests := []struct {
		name string
		// failures is how many EventsSince calls are refused before the
		// store starts answering.
		failures int
		// deadline bounds the prime; zero means the test's own context, so
		// priming runs until it succeeds.
		deadline  time.Duration
		wantErr   bool
		wantCalls int // 0 means "unbounded, do not assert"
	}{
		{
			name:      "retries until the store answers",
			failures:  3,
			wantCalls: 4,
		},
		{
			name:     "gives up when the context ends",
			failures: forever,
			deadline: 20 * time.Millisecond,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &flakyEventReader{failures: tc.failures}
			pump := &eventPump{
				store:         reader,
				log:           func(string, ...any) {},
				broadcast:     func(events.Event) {},
				primeRetryMin: time.Millisecond,
			}

			ctx := t.Context()
			if tc.deadline > 0 {
				timed, cancel := context.WithTimeout(ctx, tc.deadline)
				defer cancel()
				ctx = timed
			}

			err := pump.primeWithRetry(ctx)
			if tc.wantErr && err == nil {
				t.Fatal("primeWithRetry returned no error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("primeWithRetry: %v", err)
			}
			if tc.wantCalls > 0 && reader.calls != tc.wantCalls {
				t.Fatalf("EventsSince called %d times, want %d", reader.calls, tc.wantCalls)
			}
		})
	}
}

// A dashboard route reloaded in the browser must get the app shell. The live
// stream once sat at /events, the same path as the Events page, so a reload
// there rendered the raw event stream instead of the page.
func TestDashboardRoutesServeTheAppShell(t *testing.T) {
	ts := newSSETestServer(t, &Server{})
	for _, path := range []string{"/events", "/events?tab=inspector"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				t.Fatalf("GET %s served the event stream, want the app shell", path)
			}
		})
	}
}
