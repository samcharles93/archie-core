package webui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

func newTestSSEStream(since int64) (*sseStream, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	return &sseStream{
		log:   slog.New(slog.DiscardHandler),
		w:     rec,
		fl:    rec,
		since: since,
	}, rec
}

// sendPage is the seam catchUp's page-boundary logic depends on: it must
// stop the instant targetID is reached, even mid-page, rather than sending
// events beyond what the caller asked for.
func TestSSEStreamSendPageStopsAtTargetMidPage(t *testing.T) {
	s, _ := newTestSSEStream(0)
	backlog := []events.Event{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}

	reachedTarget, ok := s.sendPage(backlog, 3)

	if !ok {
		t.Fatal("sendPage() ok = false, want true")
	}
	if !reachedTarget {
		t.Fatal("reachedTarget = false, want true: target 3 was inside the page")
	}
	if s.since != 3 {
		t.Fatalf("since = %d, want 3 (stopped at the target, not the end of the page)", s.since)
	}
}

// A target beyond the page must not be reported reached: the caller needs
// another page.
func TestSSEStreamSendPageDoesNotReportTargetReachedBeyondThePage(t *testing.T) {
	s, _ := newTestSSEStream(0)
	backlog := []events.Event{{ID: 1}, {ID: 2}, {ID: 3}}

	reachedTarget, ok := s.sendPage(backlog, 10)

	if !ok {
		t.Fatal("sendPage() ok = false, want true")
	}
	if reachedTarget {
		t.Fatal("reachedTarget = true, want false: target 10 was never reached in this page")
	}
	if s.since != 3 {
		t.Fatalf("since = %d, want 3 (the whole page was sent)", s.since)
	}
}

// targetID == 0 means "drain everything available"; sendPage must never
// treat it as a target to stop at, or catchUp's initial call (which always
// passes 0) would stop after the very first event.
func TestSSEStreamSendPageIgnoresZeroTarget(t *testing.T) {
	s, _ := newTestSSEStream(0)
	backlog := []events.Event{{ID: 1}, {ID: 2}, {ID: 3}}

	reachedTarget, ok := s.sendPage(backlog, 0)

	if !ok || reachedTarget {
		t.Fatalf("sendPage(targetID=0) = (%v, %v), want (false, true)", reachedTarget, ok)
	}
	if s.since != 3 {
		t.Fatalf("since = %d, want 3 (the whole page was sent)", s.since)
	}
}

// A send failure must stop the page immediately and report failure, not
// silently skip the failed event and keep going.
func TestSSEStreamSendPageStopsOnWriteFailure(t *testing.T) {
	s, _ := newTestSSEStream(0)
	s.w = failingWriter{}
	backlog := []events.Event{{ID: 1}, {ID: 2}}

	reachedTarget, ok := s.sendPage(backlog, 0)

	if ok {
		t.Fatal("sendPage() ok = true, want false: the write failed")
	}
	if reachedTarget {
		t.Fatal("reachedTarget = true, want false on failure")
	}
	if s.since != 0 {
		t.Fatalf("since = %d, want 0 (unchanged: the first send already failed)", s.since)
	}
}

// failingWriter fails every Write, so send() can be tested against a dead
// connection without a real network failure.
type failingWriter struct{}

func (failingWriter) Header() http.Header        { return http.Header{} }
func (failingWriter) Write([]byte) (int, error)  { return 0, errWriteFailed }
func (failingWriter) WriteHeader(statusCode int) {}

var errWriteFailed = errors.New("write failed")

// fakeEventStore serves EventsSince from a fixed, pre-seeded slice, paginating
// the same way the real store does: up to limit events with id > sinceID.
type fakeEventStore struct {
	store.TaskStore
	all []events.Event
}

func (f *fakeEventStore) EventsSince(_ context.Context, sinceID int64, limit int) ([]events.Event, error) {
	page := make([]events.Event, 0, limit)
	for _, e := range f.all {
		if e.ID <= sinceID {
			continue
		}
		page = append(page, e)
		if len(page) >= limit {
			break
		}
	}
	return page, nil
}

// catchUp must page across more than one EventsSince fetch when the backlog
// exceeds sseBacklogPageSize in one page, rather than stopping after the
// first page as though that were everything.
func TestSSEStreamCatchUpPagesAcrossMultipleFetches(t *testing.T) {
	const total = sseBacklogPageSize + 50
	all := make([]events.Event, total)
	for i := range all {
		all[i] = events.Event{ID: int64(i + 1)}
	}

	s, rec := newTestSSEStream(0)
	s.store = &fakeEventStore{all: all}

	if !s.catchUp(context.Background(), 0) {
		t.Fatal("catchUp() = false, want true")
	}
	if s.since != int64(total) {
		t.Fatalf("since = %d, want %d (every event drained across pages)", s.since, total)
	}
	if n := countSSEFrames(rec.Body.String()); n != total {
		t.Fatalf("sent %d SSE frames, want %d", n, total)
	}
}

// A targetID landing exactly on the last event of a full page must still be
// recognised as reached, not mistaken for "page not yet exhausted, fetch
// another".
func TestSSEStreamCatchUpStopsAtTargetOnPageBoundary(t *testing.T) {
	all := make([]events.Event, sseBacklogPageSize+50)
	for i := range all {
		all[i] = events.Event{ID: int64(i + 1)}
	}

	s, _ := newTestSSEStream(0)
	s.store = &fakeEventStore{all: all}

	if !s.catchUp(context.Background(), sseBacklogPageSize) {
		t.Fatal("catchUp() = false, want true")
	}
	if s.since != sseBacklogPageSize {
		t.Fatalf("since = %d, want %d (stopped exactly at the target, not the whole backlog)", s.since, sseBacklogPageSize)
	}
}

// countSSEFrames counts "data:" lines, which appear exactly once per event
// frame (see sseStream.send's "id: ...\ndata: ...\n\n" format).
func countSSEFrames(body string) int {
	return strings.Count(body, "\ndata: ")
}
