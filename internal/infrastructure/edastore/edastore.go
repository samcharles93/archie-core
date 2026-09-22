// Package edastore persists the event-capture and playbook-binding tables on
// PocketBase-owned collections.
//
// These are the tables behind the event-capture epic: an inbound webhook is
// captured whether or not anything understands it yet, an operator inspects
// the real payload, maps fields against it, and binds a matcher and mapping
// to a workflow. That loop wants a runtime-editable store with a live view of
// arriving payloads, which is what PocketBase collections give directly.
//
// Unlike the read-only surface in internal/infrastructure/stateadmin, these
// collections are authoritative and writable: the admin UI editing a binding
// IS the feature, not a hazard. The task lifecycle tables stay on SQLite --
// they carry ordering, cursor and task-grant semantics that gain nothing from
// being hand-editable. binding_dispatches.task reaches them by plain id, which
// is the single seam between the two stores.
//
// Idempotency here is schema-enforced. The SQLite implementation relied on
// INSERT OR IGNORE against a composite primary key; a unique index does the
// same job without every caller having to remember the pattern.
package edastore

import (
	"errors"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	_ "modernc.org/sqlite"
)

// Collection names. They are unprefixed because this store owns its database;
// nothing else writes these tables.
const (
	CollCaptures           = "captures"
	CollMappings           = "mappings"
	CollBindings           = "bindings"
	CollBindingDispatches  = "binding_dispatches"
	CollPlaybookDispatches = "playbook_dispatches"
	CollToolCalls          = "tool_calls"
)

// Binding lifecycle. A binding is only live once a human approves it, which is
// an acceptance criterion of the epic rather than a convention.
const (
	StatusDraft    = "draft"
	StatusApproved = "approved"
)

var (
	// ErrBindingNotFound reports an operation naming a binding that is absent.
	ErrBindingNotFound = errors.New("edastore: binding not found")
	// ErrCaptureNotFound reports a capture that is absent or aged out.
	ErrCaptureNotFound = errors.New("edastore: capture not found")
)

// Collections returns every collection this store owns.
func Collections() []string {
	return []string{
		CollCaptures, CollMappings, CollBindings,
		CollBindingDispatches, CollPlaybookDispatches, CollToolCalls,
	}
}

// Config describes one store.
type Config struct {
	// DBPath is the SQLite file PocketBase owns for these collections.
	DBPath string
	// DataDir holds PocketBase's auxiliary database and settings.
	DataDir string
}

// Store is the PocketBase-backed EDA persistence.
type Store struct{ app *pocketbase.PocketBase }

// Open bootstraps the store and ensures its collections exist.
func Open(cfg Config) (*Store, error) {
	if cfg.DBPath == "" || cfg.DataDir == "" {
		return nil, errors.New("edastore: DBPath and DataDir are required")
	}
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  cfg.DataDir,
		HideStartBanner: true,
		DBConnect: func(requested string) (*dbx.DB, error) {
			if isDataDB(requested) {
				requested = cfg.DBPath
			}
			return dbx.Open("sqlite", requested+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
		},
	})
	if err := app.Bootstrap(); err != nil {
		return nil, fmt.Errorf("edastore: bootstrap: %w", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		return nil, fmt.Errorf("edastore: migrations: %w", err)
	}
	if err := removeDefaultAuthCollection(app); err != nil {
		return nil, err
	}
	if err := ensureCollections(app); err != nil {
		return nil, err
	}
	return &Store{app: app}, nil
}

// App exposes the PocketBase application so one process can serve this store
// and its admin UI from a single bootstrap.
func (s *Store) App() core.App { return s.app }

// Close releases the store's database connections.
func (s *Store) Close() error { return s.app.ClearBootstrap() }
