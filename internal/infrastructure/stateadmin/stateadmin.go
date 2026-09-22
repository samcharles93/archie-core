// Package stateadmin serves a read-only operator surface over the State
// Store's SQLite file: a dashboard for inspecting task state, backed by
// PocketBase.
//
// It co-tenants on archie.db rather than owning a database of its own.
// PocketBase's data.db is redirected onto the State Store's file, so its
// system tables sit beside the State Store's in one file, and PRAGMA
// user_version -- archie's migration version -- is left untouched. Running
// inside the State Store process puts the surface under the same
// store.AcquireOwnership claim, so the single-owner invariant covers it
// without a second lock.
//
// Every archie table is exposed as a PocketBase *view* collection, which is
// read-only by construction. That is the point: the StateStoreService gRPC
// contract stays the only writer of task state, and this surface cannot
// become a second one.
//
// View collections alone are not enough to hold that line. PocketBase also
// ships two superuser routes that reach past them, so both are refused here:
// POST /api/sql executes write statements as well as reads (apis.executeQuery
// detects a write and runs it anyway), and /api/backups would give the file a
// second snapshot and restore semantics next to store.Backup's VACUUM INTO.
// Refusing the routes removes them rather than leaving them behind an auth
// check, because "only a superuser can corrupt task state" is still a second
// writer.
package stateadmin

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"github.com/pocketbase/pocketbase/ui"
	_ "modernc.org/sqlite"
)

// refusedPrefixes are the PocketBase routes that would make this surface a
// second writer of State Store data. See the package comment.
var refusedPrefixes = []string{"/api/backups", "/api/sql"}

// views maps each exposed collection to the query that populates it. The
// collection name is prefixed so it cannot collide with a PocketBase system
// collection, and every query must select an "id": PocketBase keys view
// records on it, and resources is keyed by kind rather than a row id.
var views = []struct {
	name  string
	query string
}{
	{"archie_tasks", `SELECT id, owner, repo, issue_number, title, status, workflow, stage, branch, pr_number, tokens_used, iterations, attempt, park_reason, identity, created_at, updated_at FROM tasks`},
	{"archie_transitions", `SELECT id, task_id, at, from_status, to_status, detail FROM transitions`},
	{"archie_events", `SELECT id, at, kind, task_id, repo, issue, workflow, stage, attempt FROM events`},
	{"archie_bindings", `SELECT id, name, source, mapping_id, workflow, owner, repo, version, status FROM bindings`},
	{"archie_identities", `SELECT id, kind, display_name, lifecycle, version, created_at, updated_at FROM identities`},
	{"archie_resources", `SELECT kind AS id, kind, version, updated_at FROM resources`},
}

// ViewCollections returns the collection names this surface exposes.
func ViewCollections() []string {
	names := make([]string, 0, len(views))
	for _, v := range views {
		names = append(names, v.name)
	}
	return names
}

// Config describes one admin surface.
type Config struct {
	// DBPath is the State Store's SQLite file. The surface co-tenants on it.
	DBPath string
	// DataDir holds PocketBase's own auxiliary.db, settings and uploads.
	// Nothing archie owns lives here.
	DataDir string
}

// Server is a bootstrapped admin surface.
type Server struct {
	app *pocketbase.PocketBase
}

// New bootstraps the surface against cfg.DBPath and registers its view
// collections. It binds no listener: the caller serves Handler on its own,
// so this package has no lifecycle of its own to get wrong.
func New(cfg Config) (*Server, error) {
	if cfg.DBPath == "" {
		return nil, errors.New("stateadmin: DBPath is required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("stateadmin: DataDir is required")
	}
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  cfg.DataDir,
		HideStartBanner: true,
		DBConnect:       connectTo(cfg.DBPath),
	})
	if err := app.Bootstrap(); err != nil {
		return nil, fmt.Errorf("stateadmin: bootstrap: %w", err)
	}
	// apis.Serve would run these on listen; this surface is served through the
	// caller's listener instead, so it applies them itself.
	if err := app.RunAllMigrations(); err != nil {
		return nil, fmt.Errorf("stateadmin: run migrations: %w", err)
	}
	if err := removeDefaultAuthCollection(app); err != nil {
		return nil, err
	}
	if err := registerViews(app); err != nil {
		return nil, err
	}
	return &Server{app: app}, nil
}

// connectTo redirects PocketBase's data.db onto the State Store's file while
// leaving auxiliary.db where PocketBase wants it. The DSN matches the one the
// store opens with, so both connections agree on journal mode and busy
// timeout instead of racing on conflicting pragmas.
func connectTo(dbPath string) core.DBConnectFunc {
	return func(requested string) (*dbx.DB, error) {
		if filepath.Base(requested) == "data.db" {
			requested = dbPath
		}
		return dbx.Open("sqlite", requested+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	}
}

// registerViews creates or updates each view collection. It runs on every
// process start, so an existing collection is updated rather than refused.
func registerViews(app core.App) error {
	for _, v := range views {
		collection, err := app.FindCollectionByNameOrId(v.name)
		if err != nil {
			collection = core.NewViewCollection(v.name)
		} else if !collection.IsView() {
			return fmt.Errorf("stateadmin: collection %q exists and is not a view", v.name)
		}
		collection.ViewQuery = v.query
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("stateadmin: register view %q: %w", v.name, err)
		}
	}
	return nil
}

// removeDefaultAuthCollection deletes the "users" collection PocketBase
// creates on first bootstrap.
//
// Its createRule is open, so leaving it in place lets an unauthenticated
// caller POST a record and write a row into the State Store's database file.
// This surface authenticates superusers only and has no notion of end users,
// so the collection is deleted rather than given a tighter rule: a collection
// that is not there cannot be loosened again by a later PocketBase default.
func removeDefaultAuthCollection(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return nil // already absent, including on every restart after the first
	}
	if err := app.Delete(collection); err != nil {
		return fmt.Errorf("stateadmin: remove default users collection: %w", err)
	}
	return nil
}

// refuseWritePaths answers every refusedPrefixes route with 404.
func refuseWritePaths(r *router.Router[*core.RequestEvent]) {
	r.BindFunc(func(e *core.RequestEvent) error {
		for _, prefix := range refusedPrefixes {
			if strings.HasPrefix(e.Request.URL.Path, prefix) {
				return router.NewNotFoundError("", nil)
			}
		}
		return e.Next()
	})
}

// Handler builds the surface's HTTP handler without binding a listener.
//
// apis.NewRouter binds the JSON API but not the dashboard: apis.Serve mounts
// the embedded UI on /_/ itself, along with autocert and www-redirect
// machinery this surface has no use for. So the dashboard route is mounted
// here, and the caller keeps ownership of the listener.
func (s *Server) Handler() (http.Handler, error) {
	r, err := apis.NewRouter(s.app)
	if err != nil {
		return nil, err
	}
	refuseWritePaths(r)
	if ui.DistDirFS != nil {
		r.GET("/_/{path...}", apis.Static(ui.DistDirFS, false)).Bind(apis.Gzip())
	}
	return r.BuildMux()
}

// App exposes the underlying PocketBase application.
func (s *Server) App() core.App { return s.app }

// Close releases the surface's database connections.
func (s *Server) Close() error { return s.app.ClearBootstrap() }
