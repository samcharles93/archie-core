package webui

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
)

// testAt is a fixed instant every synthetic event in this file shares, so a
// cursor can be built for an event from its id alone. Two events sharing a
// timestamp exercise the id tie-break, which is the point of the cursor.
var testAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// cursor builds the opaque resume cursor for a synthetic event with id id.
func cursor(id int64) string {
	return storecontract.EventCursor(testAt, id)
}

func newTestSSEStream(since string) (*sseStream, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	return &sseStream{
		log:   slog.New(slog.DiscardHandler),
		w:     rec,
		fl:    rec,
		since: since,
	}, rec
}

// sendPage is the seam catchUp's page-boundary logic depends on: it must
// stop the instant target is reached, even mid-page, rather than sending
// events beyond what the caller asked for.
func TestSSEStreamSendPageStopsAtTargetMidPage(t *testing.T) {
	s, _ := newTestSSEStream("")
	backlog := []events.Event{
		{ID: 1, At: testAt},
		{ID: 2, At: testAt},
		{ID: 3, At: testAt},
		{ID: 4, At: testAt},
		{ID: 5, At: testAt},
	}

	reachedTarget, ok := s.sendPage(backlog, cursor(3))

	if !ok {
		t.Fatal("sendPage() ok = false, want true")
	}
	if !reachedTarget {
		t.Fatal("reachedTarget = false, want true: target 3 was inside the page")
	}
	if s.since != cursor(3) {
		t.Fatalf("since = %q, want %q (stopped at the target, not the end of the page)", s.since, cursor(3))
	}
}

// A target beyond the page must not be reported reached: the caller needs
// another page.
func TestSSEStreamSendPageDoesNotReportTargetReachedBeyondThePage(t *testing.T) {
	s, _ := newTestSSEStream("")
	backlog := []events.Event{{ID: 1, At: testAt}, {ID: 2, At: testAt}, {ID: 3, At: testAt}}

	reachedTarget, ok := s.sendPage(backlog, cursor(10))

	if !ok {
		t.Fatal("sendPage() ok = false, want true")
	}
	if reachedTarget {
		t.Fatal("reachedTarget = true, want false: target 10 was never reached in this page")
	}
	if s.since != cursor(3) {
		t.Fatalf("since = %q, want %q (the whole page was sent)", s.since, cursor(3))
	}
}

// target == "" means "drain everything available"; sendPage must never
// treat it as a target to stop at, or catchUp's initial call (which always
// passes "") would stop after the very first event.
func TestSSEStreamSendPageIgnoresZeroTarget(t *testing.T) {
	s, _ := newTestSSEStream("")
	backlog := []events.Event{{ID: 1, At: testAt}, {ID: 2, At: testAt}, {ID: 3, At: testAt}}

	reachedTarget, ok := s.sendPage(backlog, "")

	if !ok || reachedTarget {
		t.Fatalf("sendPage(target=\"\") = (%v, %v), want (false, true)", reachedTarget, ok)
	}
	if s.since != cursor(3) {
		t.Fatalf("since = %q, want %q (the whole page was sent)", s.since, cursor(3))
	}
}

// A send failure must stop the page immediately and report failure, not
// silently skip the failed event and keep going.
func TestSSEStreamSendPageStopsOnWriteFailure(t *testing.T) {
	s, _ := newTestSSEStream("")
	s.w = failingWriter{}
	backlog := []events.Event{{ID: 1, At: testAt}, {ID: 2, At: testAt}}

	reachedTarget, ok := s.sendPage(backlog, "")

	if ok {
		t.Fatal("sendPage() ok = true, want false: the write failed")
	}
	if reachedTarget {
		t.Fatal("reachedTarget = true, want false on failure")
	}
	if s.since != "" {
		t.Fatalf("since = %q, want empty (unchanged: the first send already failed)", s.since)
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
// the same way the real store does: up to limit events after cursor.
type fakeEventStore struct {
	storecontract.TaskStore
	all []events.Event
}

func (f *fakeEventStore) EventsSince(_ context.Context, since string, limit int) ([]events.Event, error) {
	page := make([]events.Event, 0, limit)
	for _, e := range f.all {
		if storecontract.EventCursor(e.At, e.ID) <= since {
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
		all[i] = events.Event{ID: int64(i + 1), At: testAt}
	}

	s, rec := newTestSSEStream("")
	s.store = &fakeEventStore{all: all}

	if !s.catchUp(context.Background(), "") {
		t.Fatal("catchUp() = false, want true")
	}
	if s.since != cursor(total) {
		t.Fatalf("since = %q, want %q (every event drained across pages)", s.since, cursor(total))
	}
	if n := countSSEFrames(rec.Body.String()); n != total {
		t.Fatalf("sent %d SSE frames, want %d", n, total)
	}
}

// Every task frame identifies its topic so one EventSource can dispatch it
// without interpreting the event's domain payload.
func TestSSETaskFrameCarriesTopic(t *testing.T) {
	stream, rec := newTestSSEStream("")
	if !stream.send(events.Event{ID: 1, At: testAt, Kind: events.KindTaskQueued}) {
		t.Fatal("send failed")
	}
	line := strings.Split(rec.Body.String(), "\n")[1]
	var frame struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Topic != "tasks" {
		t.Fatalf("topic = %q, want tasks", frame.Topic)
	}
}

// A target landing exactly on the last event of a full page must still be
// recognised as reached, not mistaken for "page not yet exhausted, fetch
// another".
func TestSSEStreamCatchUpStopsAtTargetOnPageBoundary(t *testing.T) {
	all := make([]events.Event, sseBacklogPageSize+50)
	for i := range all {
		all[i] = events.Event{ID: int64(i + 1), At: testAt}
	}

	s, _ := newTestSSEStream("")
	s.store = &fakeEventStore{all: all}

	if !s.catchUp(context.Background(), cursor(sseBacklogPageSize)) {
		t.Fatal("catchUp() = false, want true")
	}
	if s.since != cursor(sseBacklogPageSize) {
		t.Fatalf("since = %q, want %q (stopped exactly at the target, not the whole backlog)", s.since, cursor(sseBacklogPageSize))
	}
}

// countSSEFrames counts "data:" lines, which appear exactly once per event
// frame (see sseStream.send's "id: ...\ndata: ...\n\n" format).
func countSSEFrames(body string) int {
	return strings.Count(body, "\ndata: ")
}

// TestSSEStreamSendPageSkipsNoiseKindsButAdvancesSince is the regression
// test for archie-core-521: turn_completed fires on every chat turn with no
// task correlation (Detail is a raw session ID, no TaskID), and dominated
// the dashboard's Live Activity panel. It must never reach the SSE client,
// but since must still advance past it -- otherwise the next catchUp
// refetches the same filtered event forever, since EventsSince only ever
// returns events after since.
func TestSSEStreamSendPageSkipsNoiseKindsButAdvancesSince(t *testing.T) {
	s, rec := newTestSSEStream("")
	backlog := []events.Event{
		{ID: 1, At: testAt, Kind: events.KindTaskQueued},
		{ID: 2, At: testAt, Kind: events.KindTurnCompleted, Detail: "100000000"},
		{ID: 3, At: testAt, Kind: events.KindTurnCompleted, Detail: "100000000"},
		{ID: 4, At: testAt, Kind: events.KindParked},
	}

	reachedTarget, ok := s.sendPage(backlog, "")

	if !ok || reachedTarget {
		t.Fatalf("sendPage() = (%v, %v), want (false, true)", reachedTarget, ok)
	}
	if s.since != cursor(4) {
		t.Fatalf("since = %q, want %q (advanced past every event, including filtered ones)", s.since, cursor(4))
	}
	if n := countSSEFrames(rec.Body.String()); n != 2 {
		t.Fatalf("sent %d SSE frames, want 2 (turn_completed excluded)", n)
	}
	if strings.Contains(rec.Body.String(), "turn_completed") {
		t.Error("SSE body contains a turn_completed frame, want it excluded entirely")
	}
}

func TestSSEMultiTopicCursorHeaderWinsOverDeliberateReconnectURL(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/stream?topics=logs&since="+streamCursors{Tasks: cursor(1), Logs: 4}.id(), nil)
	request.Header.Set("Last-Event-ID", streamCursors{Tasks: cursor(2), Logs: 5}.id())
	got := sseCursors(request)
	if got.Tasks != cursor(2) || got.Logs != 5 {
		t.Fatalf("cursors = %+v, want tasks 2 and logs 5 from native reconnect", got)
	}
}

// TestSSESinceDegradesUnparseableCursorToBeginning is the legacy-cursor
// tolerance guard: a browser holding the pre-cursor integer id: field, or a
// ?since= carrying garbage, must resume from the beginning rather than error
// or skip. This deliberately preserves the old failed-ParseInt-yields-0
// degradation.
func TestSSESinceDegradesUnparseableCursorToBeginning(t *testing.T) {
	valid := storecontract.EventCursor(testAt, 1)
	tests := []struct {
		name   string
		since  string
		header string
		want   string
	}{
		{name: "absent", want: ""},
		{name: "legacy integer header", header: "42", want: ""},
		{name: "garbage query", since: "not-a-cursor", want: ""},
		{name: "garbage header", header: "garbage", want: ""},
		{name: "valid query cursor", since: valid, want: valid},
		{name: "valid header cursor beats garbage query", since: "garbage", header: valid, want: valid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/stream", nil)
			if tt.since != "" {
				q := req.URL.Query()
				q.Set("since", tt.since)
				req.URL.RawQuery = q.Encode()
			}
			if tt.header != "" {
				req.Header.Set("Last-Event-ID", tt.header)
			}
			if got := sseSince(req); got != tt.want {
				t.Fatalf("sseSince = %q, want %q", got, tt.want)
			}
		})
	}
}
