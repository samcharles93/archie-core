package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
)

// testBinding returns a valid binding with a sufficiently long secret for
// Validate. Tests that need a different shape build their own.
func testBinding(source string) binding.Binding {
	return binding.Binding{
		Name:      "sentry binding",
		Matcher:   binding.Matcher{Source: source},
		MappingID: 1,
		Workflow:  "implement",
		Status:    binding.StatusDraft,
		Secret:    "0123456789abcdef0123456789abcdef",
	}
}

func TestInsertBindingRoundTrips(t *testing.T) {
	s := openTest(t)
	b := testBinding("sentry")
	id, err := s.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if id == 0 {
		t.Fatalf("InsertBinding returned zero id")
	}

	got, err := s.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding: %v", err)
	}
	if got == nil {
		t.Fatalf("GetBinding returned nil for a just-inserted row")
	}
	if got.ID != id {
		t.Fatalf("round-tripped id = %d, want %d", got.ID, id)
	}
	if got.Name != b.Name || got.Workflow != b.Workflow || got.MappingID != b.MappingID {
		t.Fatalf("round-tripped fields = %+v", got)
	}
	if got.Matcher.Source != b.Matcher.Source {
		t.Fatalf("round-tripped matcher.source = %q, want %q", got.Matcher.Source, b.Matcher.Source)
	}
	if got.Status != binding.StatusDraft {
		t.Fatalf("status = %q, want %q (insert forces draft regardless of caller)", got.Status, binding.StatusDraft)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}
	if got.Secret != b.Secret {
		t.Fatalf("secret round-trip mismatch")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("CreatedAt/UpdatedAt must be stamped, got %+v", got)
	}
}

// TestInsertBindingRoundTripsOwnerRepo covers the multi-repo fix: a
// binding pinned to a specific owner/repo persists and reads back
// unchanged, and UpdateBinding can change the pin the same way it
// changes any other editable field.
func TestInsertBindingRoundTripsOwnerRepo(t *testing.T) {
	s := openTest(t)
	b := testBinding("sentry")
	b.Owner, b.Repo = "acme", "widget"
	id, err := s.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}

	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding: %+v, %v", got, err)
	}
	if got.Owner != "acme" || got.Repo != "widget" {
		t.Fatalf("round-tripped owner/repo = %q/%q, want acme/widget", got.Owner, got.Repo)
	}

	updated := *got
	updated.Owner, updated.Repo = "other-org", "other-repo"
	if err := s.UpdateBinding(t.Context(), updated); err != nil {
		t.Fatalf("UpdateBinding: %v", err)
	}
	got, err = s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after update: %+v, %v", got, err)
	}
	if got.Owner != "other-org" || got.Repo != "other-repo" {
		t.Fatalf("updated owner/repo = %q/%q, want other-org/other-repo", got.Owner, got.Repo)
	}
}

func TestOpenMigratesLegacyBindingsOwnerRepoColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(t.Context(), `
		CREATE TABLE bindings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			mapping_id INTEGER NOT NULL DEFAULT 0,
			workflow TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'draft',
			secret TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO bindings (name, source, workflow, secret, created_at, updated_at)
		VALUES ('legacy', 'sentry', 'implement', 'legacy-secret', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		PRAGMA user_version = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	got, err := s.ListBindings(t.Context())
	if err != nil {
		t.Fatalf("ListBindings after legacy migration: %v", err)
	}
	if len(got) != 1 || got[0].Name != "legacy" || got[0].Owner != "" || got[0].Repo != "" {
		t.Fatalf("legacy bindings = %+v, want preserved row with empty owner/repo", got)
	}

	created := testBinding("github")
	created.Owner, created.Repo = "acme", "widget"
	id, err := s.InsertBinding(t.Context(), created)
	if err != nil {
		t.Fatalf("InsertBinding after legacy migration: %v", err)
	}
	created.ID = id
	created.Secret = ""
	created.Owner, created.Repo = "other", "repo"
	if err := s.UpdateBinding(t.Context(), created); err != nil {
		t.Fatalf("UpdateBinding after legacy migration: %v", err)
	}
	updated, err := s.GetBinding(t.Context(), id)
	if err != nil || updated == nil {
		t.Fatalf("GetBinding after legacy migration update: %+v, %v", updated, err)
	}
	if updated.Owner != "other" || updated.Repo != "repo" {
		t.Fatalf("updated owner/repo = %q/%q, want other/repo", updated.Owner, updated.Repo)
	}
}

func TestGetBindingMissingReturnsNilNoError(t *testing.T) {
	s := openTest(t)
	got, err := s.GetBinding(t.Context(), 99999)
	if err != nil {
		t.Fatalf("GetBinding: %v", err)
	}
	if got != nil {
		t.Fatalf("GetBinding for a missing id = %+v, want nil", got)
	}
}

func TestListBindingsOrdersNewestFirst(t *testing.T) {
	s := openTest(t)
	for i, src := range []string{"alpha", "beta", "gamma"} {
		if _, err := s.InsertBinding(t.Context(), testBinding(src)); err != nil {
			t.Fatalf("InsertBinding[%d]: %v", i, err)
		}
	}
	got, err := s.ListBindings(t.Context())
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListBindings = %d rows, want 3", len(got))
	}
	if got[0].ID <= got[1].ID || got[1].ID <= got[2].ID {
		t.Fatalf("ListBindings not newest-first: ids = %d, %d, %d", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestUpdateBindingBumpsVersion(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	updated := testBinding("sentry")
	updated.ID = id
	updated.Name = "renamed"
	if err := s.UpdateBinding(t.Context(), updated); err != nil {
		t.Fatalf("UpdateBinding: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after update: %+v, %v", got, err)
	}
	if got.Version != 2 {
		t.Fatalf("version = %d, want 2 after one update", got.Version)
	}
	if got.Name != "renamed" {
		t.Fatalf("updated name = %q, want %q", got.Name, "renamed")
	}
}

func TestUpdateBindingOnArmedForcesPendingApproval(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET status='pending_approval' WHERE id=?`, id); err != nil {
		t.Fatalf("force pending_approval: %v", err)
	}
	if err := s.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding: %v", err)
	}
	armed, err := s.GetBinding(t.Context(), id)
	if err != nil || armed == nil {
		t.Fatalf("GetBinding after approve: %+v, %v", armed, err)
	}
	if armed.Status != binding.StatusArmed {
		t.Fatalf("status after approve = %q, want armed", armed.Status)
	}

	// An edit on an armed binding must drop it back to pending_approval.
	updated := *armed
	updated.Name = "edit while armed"
	if err := s.UpdateBinding(t.Context(), updated); err != nil {
		t.Fatalf("UpdateBinding on armed: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after armed edit: %+v, %v", got, err)
	}
	if got.Status != binding.StatusPendingApproval {
		t.Fatalf("status after armed edit = %q, want pending_approval", got.Status)
	}
	if got.Version <= armed.Version {
		t.Fatalf("version did not advance after update: was %d, now %d", armed.Version, got.Version)
	}
}

func TestUpdateBindingPreservesSecretWhenEmpty(t *testing.T) {
	s := openTest(t)
	b := testBinding("sentry")
	b.Secret = "0123456789abcdef0123456789abcdef"
	id, err := s.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	updated := testBinding("sentry")
	updated.ID = id
	updated.Secret = "" // empty -> preserve
	if err := s.UpdateBinding(t.Context(), updated); err != nil {
		t.Fatalf("UpdateBinding: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after update: %+v, %v", got, err)
	}
	if got.Secret != b.Secret {
		t.Fatalf("secret = %q, want preserved %q", got.Secret, b.Secret)
	}
}

func TestApproveBindingFromPendingApprovalToArmedSucceeds(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET status='pending_approval' WHERE id=?`, id); err != nil {
		t.Fatalf("force pending_approval: %v", err)
	}
	if err := s.ApproveBinding(t.Context(), id); err != nil {
		t.Fatalf("ApproveBinding: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after approve: %+v, %v", got, err)
	}
	if got.Status != binding.StatusArmed {
		t.Fatalf("status = %q, want armed", got.Status)
	}
}

func TestApproveBindingFromDraftFails(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	err = s.ApproveBinding(t.Context(), id)
	if !errors.Is(err, ErrBindingTransition) {
		t.Fatalf("ApproveBinding from draft = %v, want ErrBindingTransition", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding after failed approve: %+v, %v", got, err)
	}
	if got.Status != binding.StatusDraft {
		t.Fatalf("status after failed approve = %q, want unchanged draft", got.Status)
	}
}

func TestApproveBindingFromArmedFails(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET status='armed' WHERE id=?`, id); err != nil {
		t.Fatalf("force armed: %v", err)
	}
	err = s.ApproveBinding(t.Context(), id)
	if !errors.Is(err, ErrBindingTransition) {
		t.Fatalf("ApproveBinding from armed = %v, want ErrBindingTransition", err)
	}
}

func TestApproveBindingMissingReturnsErrBindingNotFound(t *testing.T) {
	s := openTest(t)
	err := s.ApproveBinding(t.Context(), 99999)
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("ApproveBinding on missing id = %v, want ErrBindingNotFound", err)
	}
}

func TestInsertBindingRejectsOverlap(t *testing.T) {
	s := openTest(t)
	if _, err := s.InsertBinding(t.Context(), testBinding("sentry")); err != nil {
		t.Fatalf("first InsertBinding: %v", err)
	}
	_, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if !errors.Is(err, ErrBindingOverlap) {
		t.Fatalf("second InsertBinding (same source) = %v, want ErrBindingOverlap", err)
	}
}

func TestApproveBindingRejectsOverlapAfterSecondInserted(t *testing.T) {
	s := openTest(t)
	first, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("first InsertBinding: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET status='pending_approval' WHERE id=?`, first); err != nil {
		t.Fatalf("force pending_approval: %v", err)
	}
	if err := s.ApproveBinding(t.Context(), first); err != nil {
		t.Fatalf("ApproveBinding first: %v", err)
	}

	second, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err == nil {
		// A duplicate-source insert must be rejected outright, so reaching
		// here means the overlap guard failed at insert time.
		t.Fatalf("second InsertBinding for armed source = id %d, want ErrBindingOverlap", second)
	}
	if !errors.Is(err, ErrBindingOverlap) {
		t.Fatalf("second InsertBinding = %v, want ErrBindingOverlap", err)
	}
}

func TestDeleteBindingRemovesRowAndLedgerEntries(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := s.RecordDispatch(t.Context(), tx, id, 1, 42, 7); err != nil {
		t.Fatalf("RecordDispatch: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.DeleteBinding(t.Context(), id); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("GetBinding after delete = %+v, want nil", got)
	}
	var n int
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM binding_dispatches WHERE binding_id=?`, id).Scan(&n); err != nil {
		t.Fatalf("COUNT binding_dispatches: %v", err)
	}
	if n != 0 {
		t.Fatalf("binding_dispatches rows for binding_id=%d = %d, want 0", id, n)
	}
}

func TestRecordDispatchIsIdempotent(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	ctx := t.Context()
	tx1, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx 1: %v", err)
	}
	if err := s.RecordDispatch(ctx, tx1, id, 1, 42, 7); err != nil {
		t.Fatalf("first RecordDispatch: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit 1: %v", err)
	}

	tx2, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx 2: %v", err)
	}
	err = s.RecordDispatch(ctx, tx2, id, 1, 42, 99)
	if !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("second RecordDispatch (same binding/capture) = %v, want ErrAlreadyDispatched", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("Rollback 2: %v", err)
	}
}

func TestListUndispatchedCapturesExcludesDispatched(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET status='armed' WHERE id=?`, id); err != nil {
		t.Fatalf("arm binding: %v", err)
	}

	// Capture 1: authenticated, will be dispatched -> should NOT appear.
	cap1, err := s.InsertCapture(t.Context(), CapturedEvent{
		Source: "sentry", Authenticated: true, Body: `{"id":"1"}`,
	}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture 1: %v", err)
	}
	// Capture 2: authenticated, not dispatched -> SHOULD appear.
	cap2, err := s.InsertCapture(t.Context(), CapturedEvent{
		Source: "sentry", Authenticated: true, Body: `{"id":"2"}`,
	}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture 2: %v", err)
	}
	// Capture 3: unauthenticated -> should NOT appear (matches() requires auth).
	cap3, err := s.InsertCapture(t.Context(), CapturedEvent{
		Source: "sentry", Authenticated: false, Body: `{"id":"3"}`,
	}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture 3: %v", err)
	}

	ctx := t.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := s.RecordDispatch(ctx, tx, id, 1, cap1, 100); err != nil {
		t.Fatalf("RecordDispatch: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, err := s.ListUndispatchedCaptures(ctx, []string{"sentry"}, 10)
	if err != nil {
		t.Fatalf("ListUndispatchedCaptures: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListUndispatchedCaptures = %d rows, want 1", len(got))
	}
	if got[0].ID != cap2 {
		t.Fatalf("returned capture id = %d, want %d (cap3 unauthenticated, cap1 dispatched)", got[0].ID, cap2)
	}
	_ = cap3
}

func TestListUndispatchedCapturesEmptySourcesReturnsNil(t *testing.T) {
	s := openTest(t)
	got, err := s.ListUndispatchedCaptures(t.Context(), nil, 10)
	if err != nil {
		t.Fatalf("ListUndispatchedCaptures(nil sources): %v", err)
	}
	if got != nil {
		t.Fatalf("ListUndispatchedCaptures(nil sources) = %+v, want nil", got)
	}
}

// TestRecordDispatchViaExplicitTx confirms RecordDispatch accepts an
// external *sql.Tx (its primary contract: caller-owned transaction).
// Without this signature the dispatch ledger and the tasks row created
// from a binding could not commit atomically.
func TestRecordDispatchViaExplicitTx(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertBinding(t.Context(), testBinding("sentry"))
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := s.RecordDispatch(ctx, tx, id, 1, 1, 1); err != nil {
		t.Fatalf("RecordDispatch via *sql.Tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}
