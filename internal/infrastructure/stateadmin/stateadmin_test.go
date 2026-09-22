package stateadmin_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/stateadmin"
	"github.com/samcharles93/archie-core/internal/store"
)

// openArchieDB creates a real, migrated State Store database and returns its
// path. The admin surface co-tenants on this exact file, so the tests exercise
// the production schema rather than a stand-in.
func openArchieDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archie.db-tasks.sqlite")
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return path
}

func newAdmin(t *testing.T, dbPath string) *stateadmin.Server {
	t.Helper()
	srv, err := stateadmin.New(stateadmin.Config{
		DBPath:  dbPath,
		DataDir: filepath.Join(t.TempDir(), "pb_data"),
	})
	if err != nil {
		t.Fatalf("stateadmin.New() error = %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// TestViewCollectionsExposeStateTables pins that every table the admin surface
// claims to expose is registered as a *view* collection. View collections are
// read-only by construction, which is what keeps the gRPC contract the only
// writer of task state.
func TestViewCollectionsExposeStateTables(t *testing.T) {
	srv := newAdmin(t, openArchieDB(t))

	for _, name := range stateadmin.ViewCollections() {
		collection, err := srv.App().FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("FindCollectionByNameOrId(%q) error = %v, want the collection to exist", name, err)
		}
		if !collection.IsView() {
			t.Errorf("collection %q type = %q, want a view: a base collection would let the admin UI write task state behind the gRPC contract", name, collection.Type)
		}
	}
}

// TestBootstrapPreservesSchemaVersion pins that PocketBase's own bootstrap does
// not disturb archie's migration versioning. Both systems share one file;
// PRAGMA user_version is archie's and must survive co-tenancy untouched.
func TestBootstrapPreservesSchemaVersion(t *testing.T) {
	path := openArchieDB(t)

	before, err := store.ValidateFile(t.Context(), path)
	if err != nil {
		t.Fatalf("ValidateFile() before admin bootstrap error = %v", err)
	}

	newAdmin(t, path)

	after, err := store.ValidateFile(t.Context(), path)
	if err != nil {
		t.Fatalf("ValidateFile() after admin bootstrap error = %v, want the store to still validate", err)
	}
	if before != after {
		t.Errorf("schema version moved across admin bootstrap: before = %d, after = %d", before, after)
	}
}

// TestStoreWritesSurviveCoTenancy is the regression that matters: archie's
// conditional-UPDATE optimistic concurrency must keep working, and keep
// reporting ErrStaleTransition for a losing writer, while the admin surface
// holds its own connections to the same file.
func TestStoreWritesSurviveCoTenancy(t *testing.T) {
	path := openArchieDB(t)
	newAdmin(t, path)

	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open() alongside admin error = %v", err)
	}
	defer st.Close()

	if _, err := st.EnqueueIssue(t.Context(), "o", "r", 1, "co-tenancy", "", "", ""); err != nil {
		t.Fatalf("EnqueueIssue() while admin holds the file error = %v", err)
	}
	queued, err := st.TaskByIssue(t.Context(), "o", "r", 1)
	if err != nil {
		t.Fatalf("TaskByIssue() error = %v", err)
	}
	id := queued.ID

	if err := st.Transition(t.Context(), id, workflow.StatusQueued, workflow.StatusRunning, ""); err != nil {
		t.Fatalf("Transition(queued->running) error = %v, want the write to commit under co-tenancy", err)
	}
	// The same transition again must lose: the row is no longer queued.
	if err := st.Transition(t.Context(), id, workflow.StatusQueued, workflow.StatusRunning, ""); !errors.Is(err, store.ErrStaleTransition) {
		t.Fatalf("second Transition() error = %v, want ErrStaleTransition: optimistic concurrency must not degrade under co-tenancy", err)
	}
}

// TestWriteReachingRoutesAreRefused pins that the routes which reach past the
// read-only view collections are gone, not merely authenticated. POST /api/sql
// executes write statements (apis.executeQuery runs a detected write anyway),
// and /api/backups would add a second snapshot and restore path beside
// store.Backup. Both are superuser-gated upstream, which is not the same as
// absent: a superuser writing task state is still a second writer.
func TestWriteReachingRoutesAreRefused(t *testing.T) {
	srv := newAdmin(t, openArchieDB(t))

	mux, err := srv.Handler()
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	// The method must match the route PocketBase actually binds: a GET against
	// the POST-only /api/sql would 404 on method alone and assert nothing.
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/backups"},
		{http.MethodGet, "/api/backups/some-snapshot"},
		{http.MethodPost, "/api/sql"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), route.method, route.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404: the gRPC contract is the only writer and store.Backup the only snapshot path", route.method, route.path, rec.Code)
		}
	}
}

// TestViewCollectionsAreIdempotent pins that a restart re-registers cleanly.
// The admin surface bootstraps on every process start, so a second pass over an
// already-populated database must update rather than fail.
func TestViewCollectionsAreIdempotent(t *testing.T) {
	path := openArchieDB(t)
	newAdmin(t, path)
	newAdmin(t, path)
}

// TestDashboardIsServed pins that the operator dashboard is actually reachable.
// apis.NewRouter binds the JSON API only; the embedded UI is mounted by
// apis.Serve, which this surface does not use. Without an explicit mount /_/
// answers 404 and the surface has an API but no dashboard, which is the whole
// reason it exists.
func TestDashboardIsServed(t *testing.T) {
	srv := newAdmin(t, openArchieDB(t))

	mux, err := srv.Handler()
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/_/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /_/ status = %d, want 200: the dashboard is the surface's reason to exist", rec.Code)
	}
}

// TestNoOpenRegistration pins that the surface has no self-service signup.
// PocketBase creates a "users" auth collection on first bootstrap whose
// createRule is open, so an unauthenticated caller could POST a record and
// write a row into the State Store's own database file. The surface is
// superuser-only by design, so the collection is removed rather than left
// with a tightened rule: a collection that does not exist cannot regress.
func TestNoOpenRegistration(t *testing.T) {
	srv := newAdmin(t, openArchieDB(t))

	if _, err := srv.App().FindCollectionByNameOrId("users"); err == nil {
		t.Error("the default users collection still exists; it accepts unauthenticated record creation into archie's database file")
	}

	mux, err := srv.Handler()
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	body := strings.NewReader(`{"email":"nobody@example.com","password":"hunter2hunter2","passwordConfirm":"hunter2hunter2"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections/users/records", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Errorf("POST /api/collections/users/records status = %d, want a refusal: unauthenticated writes must not reach the State Store's file", rec.Code)
	}
}
