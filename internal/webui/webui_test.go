package webui

// The dashboard moved from a hand-written index.html embedded in this package
// to the Vite build in ui/. Four tests were removed with it:
//
//   - TestIndexContainsViewportMeta, TestIndexHasResponsiveFeatures asserted
//     markup and CSS that now live in ui/index.html and ui/src/css/.
//   - TestIndexEscapesSingleQuotesInOnclick, TestIndexEscDoesNotEscapeSingleQuotes
//     guarded against injection through string-interpolated onclick handlers.
//     That bug is no longer reachable: ui/src/base/dom.js builds nodes with
//     createTextNode and addEventListener, so no markup is assembled from
//     strings at all.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{Store: s, Log: slog.New(slog.DiscardHandler)}
}

// stubStore wraps a real TaskStore and overrides selected methods to
// inject errors. Methods that are not overridden delegate to the inner
// store via the embedded interface.
type stubStore struct {
	store.TaskStore
	workflowStatsErr error
	stageStatsErr    error
	tokensByDayErr   error
	eventsSinceErr   error
	taskEventsErr    error
	taskByIDErr      error
	requeueErr       error
}

func (s *stubStore) TaskByID(ctx context.Context, id int64) (*store.Task, error) {
	if s.taskByIDErr != nil {
		return nil, s.taskByIDErr
	}
	return s.TaskStore.TaskByID(ctx, id)
}

func (s *stubStore) Requeue(ctx context.Context, id int64, from, workflow string) error {
	if s.requeueErr != nil {
		return s.requeueErr
	}
	return s.TaskStore.Requeue(ctx, id, from, workflow)
}

func (s *stubStore) WorkflowStats(ctx context.Context) ([]store.WorkflowStat, error) {
	if s.workflowStatsErr != nil {
		return nil, s.workflowStatsErr
	}
	return s.TaskStore.WorkflowStats(ctx)
}

func (s *stubStore) StageStats(ctx context.Context) ([]store.StageStat, error) {
	if s.stageStatsErr != nil {
		return nil, s.stageStatsErr
	}
	return s.TaskStore.StageStats(ctx)
}

func (s *stubStore) TokensByDay(ctx context.Context, days int) ([]store.DayTokens, error) {
	if s.tokensByDayErr != nil {
		return nil, s.tokensByDayErr
	}
	return s.TaskStore.TokensByDay(ctx, days)
}

func (s *stubStore) EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error) {
	if s.eventsSinceErr != nil {
		return nil, s.eventsSinceErr
	}
	return s.TaskStore.EventsSince(ctx, sinceID, limit)
}

func (s *stubStore) TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error) {
	if s.taskEventsErr != nil {
		return nil, s.taskEventsErr
	}
	return s.TaskStore.TaskEvents(ctx, taskID)
}

func TestHandleSummary(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"statuses", "workflows", "stages", "tokens_by_day"} {
		if _, ok := got[key]; !ok {
			t.Errorf("summary missing key %q: %+v", key, got)
		}
	}
}

// TestHandleSummaryWorkflowStatsError proves that handleSummary does not
// silently discard an error from WorkflowStats.  Today the handler uses
// blank-identifier assignment (workflows, _ := ...), so this test FAILS
// (wants 500, gets 200 with nil workflows).
func TestHandleSummaryWorkflowStatsError(t *testing.T) {
	base := newTestServer(t)
	ctx := t.Context()
	srv := &Server{
		Store: &stubStore{
			TaskStore:        base.Store,
			workflowStatsErr: fmt.Errorf("db locked"),
		},
		Log: base.Log,
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — WorkflowStats error was silently discarded", w.Code)
	}
}

// TestHandleSummaryStageStatsError proves that handleSummary does not
// silently discard an error from StageStats.
func TestHandleSummaryStageStatsError(t *testing.T) {
	base := newTestServer(t)
	ctx := t.Context()
	srv := &Server{
		Store: &stubStore{
			TaskStore:     base.Store,
			stageStatsErr: fmt.Errorf("db locked"),
		},
		Log: base.Log,
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — StageStats error was silently discarded", w.Code)
	}
}

// TestHandleSummaryTokensByDayError proves that handleSummary does not
// silently discard an error from TokensByDay.
func TestHandleSummaryTokensByDayError(t *testing.T) {
	base := newTestServer(t)
	ctx := t.Context()
	srv := &Server{
		Store: &stubStore{
			TaskStore:      base.Store,
			tokensByDayErr: fmt.Errorf("db locked"),
		},
		Log: base.Log,
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — TokensByDay error was silently discarded", w.Code)
	}
}

func TestHandleTasks(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "hello", "b", "", ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var got []store.Task
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "hello" {
		t.Fatalf("tasks = %+v", got)
	}
}

func TestHandleTask(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if _, err := srv.Store.InsertEvent(ctx, events.Event{Kind: "stage_start", TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks/"+strconv.FormatInt(task.ID, 10), nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var got []events.Event
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "stage_start" {
		t.Fatalf("task events = %+v", got)
	}
}

func TestHandleTaskBadID(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks/not-a-number", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleIndex(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
}

// TestHealthzIsReachableWithoutAToken: the update watchdog script polls
// this endpoint from the local host after restarting archied, with no
// dashboard session -- it must never require the dashboard token, unlike
// every other route.
func TestHealthzIsReachableWithoutAToken(t *testing.T) {
	srv := newTestServer(t)
	srv.Token = "dashboard-secret"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
}

// TestHandleSSEEventsSinceError proves that handleSSE does not silently
// discard an error from EventsSince.  Today the handler has no else
// branch for err != nil, so the error is swallowed and the client
// receives no signal that catch-up was skipped: this test FAILS.
func TestHandleSSEEventsSinceError(t *testing.T) {
	actual := newTestServer(t)
	srv := &Server{
		Store: &stubStore{
			TaskStore:      actual.Store,
			eventsSinceErr: fmt.Errorf("db locked"),
		},
		Log: actual.Log,
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events?since=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("EventsSince error silently swallowed — SSE handler never responded: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	// The SSE stream must surface the EventsSince error to the client.
	// Today the error is silently swallowed, so no error line is ever
	// sent and the test FAILS.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "error") {
			return // PASS: error was surfaced to the client
		}
	}
	t.Fatal("EventsSince error was silently swallowed — no error sent to SSE client")
}

func TestHandleSSEUsesLastEventIDForCatchUp(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	first, err := srv.Store.InsertEvent(ctx, events.Event{Kind: "log", Detail: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.InsertEvent(ctx, events.Event{Kind: "log", Detail: "second"}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", strconv.FormatInt(first, 10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("second event not received after Last-Event-ID: %v", readErr)
		}
		if strings.HasPrefix(line, "data:") {
			if strings.Contains(line, "first") {
				t.Fatalf("replayed event before Last-Event-ID: %q", line)
			}
			if strings.Contains(line, "second") {
				return
			}
		}
	}
	t.Fatal("second event not received after Last-Event-ID")
}

func TestHandleSSEBacklogAndLive(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	id, err := srv.Store.InsertEvent(ctx, events.Event{Kind: "log", Detail: "backlog"})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// The request carries t.Context() so the SSE stream is torn down when
	// the test ends rather than leaking the connection and its goroutine.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	line, err := readLineUntil(reader, "backlog")
	if err != nil {
		t.Fatalf("backlog event not received: %v (%q)", err, line)
	}

	// Now broadcast a live event and confirm it's delivered too. Give
	// the SSE handler a moment to register its subscriber.
	time.Sleep(50 * time.Millisecond)
	srv.Broadcast(events.Event{ID: id + 1, Kind: "log", Detail: "live"})

	if _, err := readLineUntil(reader, "live"); err != nil {
		t.Fatalf("live event not received: %v", err)
	}
}

// TestHandleSSEFiltersTurnCompletedFromLiveBroadcast covers the live half of
// the archie-core-521 fix: sendPage's filtering is exercised directly in
// sse_test.go, but the live broadcast path (drain) has its own send call.
// A turn_completed broadcast between two ordinary events must never reach
// the client, while the ordinary events either side of it still do.
func TestHandleSSEFiltersTurnCompletedFromLiveBroadcast(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	id, err := srv.Store.InsertEvent(ctx, events.Event{Kind: "log", Detail: "backlog"})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	if _, err := readLineUntil(reader, "backlog"); err != nil {
		t.Fatalf("backlog event not received: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	srv.Broadcast(events.Event{ID: id + 1, Kind: events.KindTurnCompleted, Detail: "100000000"})
	srv.Broadcast(events.Event{ID: id + 2, Kind: "log", Detail: "after-noise"})

	seen, err := readLinesUntil(reader, "after-noise")
	if err != nil {
		t.Fatalf("event after the filtered one was not received: %v", err)
	}
	for _, line := range seen {
		if strings.Contains(line, "turn_completed") {
			t.Fatalf("the filtered event's frame reached the client: %q", line)
		}
	}
}

// readLineUntil scans SSE "data:" lines until one contains want.
func readLineUntil(r *bufio.Reader, want string) (string, error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			return line, err
		}
		if strings.HasPrefix(line, "data:") && strings.Contains(line, want) {
			return line, nil
		}
	}
	return "", io.EOF
}

// readLinesUntil scans SSE "data:" lines, collecting every one seen, until
// one contains want -- unlike readLineUntil, callers get the full scan, not
// just the matched line, so a frame that arrived before the match but was
// never asserted on can't hide there.
func readLinesUntil(r *bufio.Reader, want string) ([]string, error) {
	var seen []string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			return seen, err
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		seen = append(seen, line)
		if strings.Contains(line, want) {
			return seen, nil
		}
	}
	return seen, io.EOF
}
