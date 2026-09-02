package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/store"
)

// Test 30: an armed binding + an authenticated capture creates a task
// whose provenance points back at the binding (and its version), and
// records a dispatch row in the dedup ledger.
func TestDispatchBindingsCreatesTaskFromArmedCapture(t *testing.T) {
	s := openDispatchTestStore(t)
	mappingID := seedMapping(t, s, "sentry", mapping.Field{Name: "title", Path: "title", Type: mapping.TypeString, Required: true})
	bindingID, bindingVersion := seedArmedBinding(t, s, "sentry", mappingID)
	seedCapture(t, s, "sentry", true, `{"title":"hello"}`)

	d := newDispatchDaemon(t, s)
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Tasks len = %d, want 1", len(tasks))
	}
	got := tasks[0]
	if got.BindingID != bindingID {
		t.Errorf("task.BindingID = %d, want %d", got.BindingID, bindingID)
	}
	if got.BindingVersion != bindingVersion {
		t.Errorf("task.BindingVersion = %d, want %d", got.BindingVersion, bindingVersion)
	}
	if got.Source != store.SourceChat {
		t.Errorf("task.Source = %q, want %q", got.Source, store.SourceChat)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("task.Status = %q, want %q", got.Status, store.StatusQueued)
	}

	assertDispatchRecorded(t, s, bindingID, got.ID)
}

// TestDispatchBindingsUsesBindingRepoPinWithMultipleConfiguredRepos is the
// multi-repo fix: a binding that pins Owner/Repo dispatches to that
// target even when the daemon has zero or several [[repos]] configured,
// where resolveBindingRepo's old single-repo-only fallback would have
// refused entirely.
func TestDispatchBindingsUsesBindingRepoPinWithMultipleConfiguredRepos(t *testing.T) {
	s := openDispatchTestStore(t)
	mappingID := seedMapping(t, s, "sentry", mapping.Field{Name: "title", Path: "title", Type: mapping.TypeString, Required: true})
	bindingID, _ := seedArmedBindingWithRepo(t, s, "sentry", mappingID, "acme", "widget")
	seedCapture(t, s, "sentry", true, `{"title":"hello"}`)

	d := newDispatchDaemonWithRepos(t, s, []config.Repo{
		{Owner: "acme", Name: "widget"},
		{Owner: "other-org", Name: "other-repo"},
	})
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Tasks len = %d, want 1 (pinned binding must dispatch despite 2 configured repos)", len(tasks))
	}
	if tasks[0].Owner != "acme" || tasks[0].Repo != "widget" {
		t.Fatalf("task owner/repo = %s/%s, want acme/widget (the binding's own pin, not a guess)", tasks[0].Owner, tasks[0].Repo)
	}
	assertDispatchRecorded(t, s, bindingID, tasks[0].ID)
}

// TestDispatchBindingsRefusesUnpinnedBindingWithMultipleConfiguredRepos
// pins the existing safety behaviour: a binding with no Owner/Repo pin
// must still refuse to dispatch when the target repo is ambiguous,
// exactly as it did before this field existed.
func TestDispatchBindingsRefusesUnpinnedBindingWithMultipleConfiguredRepos(t *testing.T) {
	s := openDispatchTestStore(t)
	mappingID := seedMapping(t, s, "sentry", mapping.Field{Name: "title", Path: "title", Type: mapping.TypeString, Required: true})
	seedArmedBinding(t, s, "sentry", mappingID) // no owner/repo pin
	seedCapture(t, s, "sentry", true, `{"title":"hello"}`)

	d := newDispatchDaemonWithRepos(t, s, []config.Repo{
		{Owner: "acme", Name: "widget"},
		{Owner: "other-org", Name: "other-repo"},
	})
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("Tasks len = %d, want 0 (an unpinned binding must still refuse an ambiguous target)", len(tasks))
	}
}

// Test 31: the binding_dispatches ledger is the at-most-once
// guarantee across cycles -- running dispatchBindings twice must
// yield exactly one task even though the undispatched-captures query
// keeps finding the capture on the second pass.
func TestDispatchBindingsDoesNotDoubleDispatch(t *testing.T) {
	s := openDispatchTestStore(t)
	mappingID := seedMapping(t, s, "sentry", mapping.Field{Name: "title", Path: "title", Type: mapping.TypeString})
	seedArmedBinding(t, s, "sentry", mappingID)
	seedCapture(t, s, "sentry", true, `{"title":"hello"}`)

	d := newDispatchDaemon(t, s)
	d.dispatchBindings(t.Context())
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("after two cycles, Tasks len = %d, want 1 (ledger dedup)", len(tasks))
	}
}

// Test 32: an unauthenticated capture can never spawn a task -- the
// matcher refuses it regardless of source match (binding.Matcher.Matches
// gates on authenticated, see docs/prds/webhook-intake-security.md).
func TestDispatchBindingsSkipsUnauthenticatedCaptures(t *testing.T) {
	s := openDispatchTestStore(t)
	mappingID := seedMapping(t, s, "sentry", mapping.Field{Name: "title", Path: "title", Type: mapping.TypeString})
	seedArmedBinding(t, s, "sentry", mappingID)
	seedCapture(t, s, "sentry", false, `{"title":"hello"}`)

	d := newDispatchDaemon(t, s)
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("unauthenticated capture spawned %d task(s), want 0", len(tasks))
	}
}

// Test 33: a required field whose path is missing in the capture body
// blocks the dispatch -- the failure path emits a
// binding_dispatch_failure event and creates no task.
func TestDispatchBindingsSkipsWhenResolveFails(t *testing.T) {
	s := openDispatchTestStore(t)
	mappingID := seedMapping(t, s, "sentry",
		mapping.Field{Name: "title", Path: "title", Type: mapping.TypeString, Required: true})
	seedArmedBinding(t, s, "sentry", mappingID)
	// Body is missing the required "title" path.
	seedCapture(t, s, "sentry", true, `{"other":"value"}`)

	d := newDispatchDaemon(t, s)
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("required-field failure spawned %d task(s), want 0", len(tasks))
	}
	events, err := s.EventsSince(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "binding_dispatch_failure" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("binding_dispatch_failure event not published; got %d events", len(events))
	}
}

// Test 34: with no armed bindings the loop is a no-op -- even with
// authenticated captures sitting in the table.
func TestDispatchBindingsNoOpWhenNoArmedBindings(t *testing.T) {
	s := openDispatchTestStore(t)
	// Capture is present, but no binding rows exist.
	seedCapture(t, s, "sentry", true, `{"title":"hello"}`)

	d := newDispatchDaemon(t, s)
	d.dispatchBindings(t.Context())

	tasks, err := s.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("no-armed-bindings loop spawned %d task(s), want 0", len(tasks))
	}
}

// Test: dispatchBindings is a no-op when Bindings is nil -- the loop
// must degrade silently rather than panic in legacy compositions.
func TestDispatchBindingsNilBindingsIsNoOp(t *testing.T) {
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{}),
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	// Should not panic.
	d.dispatchBindings(t.Context())
}

// ── helpers ──────────────────────────────────────────────────────

// openDispatchTestStore creates a fresh in-memory SQLite store for
// each dispatch test. The TempDir anchor matches store.OpenTest's
// own convention; tests rely on the same schema-migration path
// production uses (CREATE TABLE + ALTER TABLE migrations), so
// binding_id / binding_version / binding_dispatches all exist on the
// store by the time a test runs.
func openDispatchTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "dispatch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedMapping inserts a mapping with the given fields and returns its id.
// The Name field is "sentry-mapping" by default; callers pass fields
// inline.
func seedMapping(t *testing.T, s *store.Store, sourceHint string, fields ...mapping.Field) int64 {
	t.Helper()
	id, err := s.InsertMapping(t.Context(), mapping.Mapping{
		Name:       sourceHint + "-mapping",
		SourceHint: sourceHint,
		Fields:     fields,
	})
	if err != nil {
		t.Fatalf("InsertMapping: %v", err)
	}
	return id
}

// seedArmedBinding inserts a binding and walks it draft -> pending_approval
// -> armed, matching the operator flow an Approve button on the
// dashboard drives. InsertBinding forces status='draft' and the only
// sanctioned armed transition is ApproveBinding, so the helper
// advances through pending_approval via the store's test-only DB
// accessor -- production callers must use the structured methods
// (bindings.go) rather than touching the SQL directly.
func seedArmedBinding(t *testing.T, s *store.Store, source string, mappingID int64) (int64, int) {
	t.Helper()
	return seedArmedBindingWithRepo(t, s, source, mappingID, "", "")
}

// seedArmedBindingWithRepo is seedArmedBinding with an explicit
// owner/repo pin (pass "", "" for the unpinned case seedArmedBinding
// covers).
func seedArmedBindingWithRepo(t *testing.T, s *store.Store, source string, mappingID int64, owner, repo string) (int64, int) {
	t.Helper()
	id, err := s.InsertBinding(t.Context(), binding.Binding{
		Name:      "test " + source,
		Matcher:   binding.Matcher{Source: source},
		MappingID: mappingID,
		Workflow:  "implement",
		Owner:     owner,
		Repo:      repo,
		Secret:    "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if _, err := s.DB().ExecContext(t.Context(), `UPDATE bindings SET status='pending_approval' WHERE id=?`, id); err != nil {
		t.Fatalf("force pending_approval: %v", err)
	}
	if err := s.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after approve: %+v, %v", got, err)
	}
	return id, got.Version
}

// seedCapture inserts an inbound capture event with the given source,
// authenticated flag, and body. retention=0, maxEvents=0 disable
// the per-insert pruning the production path uses, so a test that
// only needs a single row in the table is not at the mercy of
// maxEvents=0's "no cap" reading.
func seedCapture(t *testing.T, s *store.Store, source string, authenticated bool, body string) {
	t.Helper()
	_, err := s.InsertCapture(t.Context(), store.CapturedEvent{
		Source:        source,
		Body:          body,
		Authenticated: authenticated,
		ReceivedAt:    time.Now().UTC(),
	}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture: %v", err)
	}
}

// newDispatchDaemon builds a Daemon whose store-related handles all
// point at the same *store.Store so dispatchBindings can resolve
// captures through the dispatcher handles and mappings through
// d.Mappings. Exactly one repo is configured so resolveBindingRepo's
// single-repo fallback accepts the dispatch; with zero or many
// configured repos the loop would log and skip, masking the test's
// positive assertions.
func newDispatchDaemon(t *testing.T, s *store.Store) *Daemon {
	t.Helper()
	return newDispatchDaemonWithRepos(t, s, []config.Repo{{Owner: "acme", Name: "widget"}})
}

// newDispatchDaemonWithRepos is newDispatchDaemon with an explicit repo
// list, for tests exercising resolveBindingRepo's owner/repo-pin path
// against zero, one, or several configured repos.
func newDispatchDaemonWithRepos(t *testing.T, s *store.Store, repos []config.Repo) *Daemon {
	t.Helper()
	cfg := config.Config{Repos: repos}
	return &Daemon{
		Cfg:                config.NewHolder(cfg),
		Store:              s,
		Mappings:           s,
		Bindings:           s,
		BindingDispatcher:  s,
		BindingTaskCreator: s,
		Log:                slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
}

// assertDispatchRecorded reads the dedup ledger directly: the
// (binding_id, capture_id) primary key is what guarantees no double
// dispatch. Going through the SQL table rather than the public
// BindingStore surface keeps the assertion focused on the contract
// Phase E actually writes to, not on a ListDispatches helper that
// may not exist.
func assertDispatchRecorded(t *testing.T, s *store.Store, bindingID, taskID int64) {
	t.Helper()
	var gotBindingID, gotTaskID int64
	if err := s.DB().QueryRowContext(t.Context(),
		`SELECT binding_id, task_id FROM binding_dispatches WHERE binding_id=?`, bindingID,
	).Scan(&gotBindingID, &gotTaskID); err != nil {
		t.Fatalf("binding_dispatches row missing: %v", err)
	}
	if gotBindingID != bindingID || gotTaskID != taskID {
		t.Fatalf("binding_dispatches row = (%d, %d), want (%d, %d)", gotBindingID, gotTaskID, bindingID, taskID)
	}
}
