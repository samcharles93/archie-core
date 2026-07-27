package webui

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
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// stubStore wraps a real TaskStore and overrides selected methods to
// inject errors. Methods that are not overridden delegate to the inner
// store via the embedded interface.
type stubStore struct {
	store.TaskStore
	workflowStatsErr error
	stageStatsErr    error
	tokensByDayErr   error
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

func TestHandleSummary(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/summary", nil)
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
	real := newTestServer(t)
	srv := &Server{
		Store: &stubStore{
			TaskStore:        real.Store,
			workflowStatsErr: fmt.Errorf("db locked"),
		},
		Log: real.Log,
	}

	req := httptest.NewRequest("GET", "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — WorkflowStats error was silently discarded", w.Code)
	}
}

// TestHandleSummaryStageStatsError proves that handleSummary does not
// silently discard an error from StageStats.
func TestHandleSummaryStageStatsError(t *testing.T) {
	real := newTestServer(t)
	srv := &Server{
		Store: &stubStore{
			TaskStore:     real.Store,
			stageStatsErr: fmt.Errorf("db locked"),
		},
		Log: real.Log,
	}

	req := httptest.NewRequest("GET", "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — StageStats error was silently discarded", w.Code)
	}
}

// TestHandleSummaryTokensByDayError proves that handleSummary does not
// silently discard an error from TokensByDay.
func TestHandleSummaryTokensByDayError(t *testing.T) {
	real := newTestServer(t)
	srv := &Server{
		Store: &stubStore{
			TaskStore:      real.Store,
			tokensByDayErr: fmt.Errorf("db locked"),
		},
		Log: real.Log,
	}

	req := httptest.NewRequest("GET", "/api/summary", nil)
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

	req := httptest.NewRequest("GET", "/api/tasks", nil)
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

	req := httptest.NewRequest("GET", "/api/tasks/"+strconv.FormatInt(task.ID, 10), nil)
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
	req := httptest.NewRequest("GET", "/api/tasks/not-a-number", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleIndex(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestIndexContainsViewportMeta(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<meta name="viewport" content="width=device-width, initial-scale=1">`) {
		t.Error("viewport meta missing in rendered HTML")
	}
}

func TestIndexHasResponsiveFeatures(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	reqs := []struct {
		name string
		want string
	}{
		{"section-table", ".section-table { overflow-x: auto; }"},
		{"feed-wraps", "white-space: normal"},
		{"feed-break-word", "overflow-wrap: break-word"},
		{"700px-breakpoint", "@media (max-width: 700px)"},
		{"950px-breakpoint", "@media (max-width: 950px)"},
		{"viewport-meta", `<meta name="viewport" content="width=device-width, initial-scale=1">`},
	}
	for _, r := range reqs {
		if !strings.Contains(body, r.want) {
			t.Errorf("missing responsive feature %q: %q", r.name, r.want)
		}
	}
}

func TestHandleSSEBacklogAndLive(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	id, err := srv.Store.InsertEvent(ctx, events.Event{Kind: "log", Detail: "backlog"})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/events")
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

func TestIndexEscapesSingleQuotesInOnclick(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()

	// escJS helper must be defined.
	if !strings.Contains(body, "escJS") {
		t.Fatal("escJS helper not found in HTML")
	}

	// The onclick template must use escJS (not esc) for owner and
	// repo  --  the values interpolated into the single-quoted JS str.
	if !strings.Contains(body, "escJS(t.owner)") {
		t.Error("onclick does not use escJS for owner")
	}
	if !strings.Contains(body, "escJS(t.repo)") {
		t.Error("onclick does not use escJS for repo")
	}
}

func TestIndexEscDoesNotEscapeSingleQuotes(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()

	// esc() must NOT escape single quotes  --  it's used for HTML
	// content/attribute contexts where apostrophes are legitimate
	// (e.g. issue titles like "Don't panic").
	idx := strings.Index(body, "const esc =")
	if idx == -1 {
		t.Fatal("esc definition not found")
	}
	snippet := body[idx : idx+200]
	// The regex should escape & < > " but NOT '
	if !strings.Contains(snippet, "[&<>\"") {
		t.Error("esc regex character class is missing or malformed")
	}
	// The character class must NOT include a single quote.
	reStart := strings.Index(snippet, "[&<>\"")
	reEnd := strings.Index(snippet[reStart:], "]")
	if reEnd == -1 {
		t.Fatal("could not find end of esc regex character class")
	}
	charClass := snippet[reStart : reStart+reEnd]
	if strings.Contains(charClass, "'") {
		t.Error("esc() regex escapes single quotes  --  this would cause backslash artifacts in HTML text")
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
