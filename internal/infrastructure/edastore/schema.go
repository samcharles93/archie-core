package edastore

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pocketbase/pocketbase/core"
)

// isDataDB reports whether PocketBase is asking for its main database, as
// opposed to the auxiliary one it keeps for logs and settings.
func isDataDB(path string) bool { return filepath.Base(path) == "data.db" }

// removeDefaultAuthCollection deletes the open-signup "users" collection
// PocketBase creates on first bootstrap. This store has no end users, and its
// createRule is open, so leaving it would accept unauthenticated writes.
func removeDefaultAuthCollection(app core.App) error {
	c, err := app.FindCollectionByNameOrId("users")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("edastore: look up default users collection: %w", err)
	}
	if err := app.Delete(c); err != nil {
		return fmt.Errorf("edastore: remove default users collection: %w", err)
	}
	return nil
}

// ensureCollections creates each collection once and is safe to re-run: the
// store bootstraps on every process start.
func ensureCollections(app core.App) error {
	for _, build := range []func(core.App) error{
		ensureCaptures, ensureMappings, ensureBindings,
		ensureBindingDispatches, ensurePlaybookDispatches, ensureToolCalls,
	} {
		if err := build(app); err != nil {
			return err
		}
	}
	return nil
}

// collection returns the named collection, or a new base collection when it
// does not exist yet. A lookup that failed for any other reason is an error:
// creating a second collection over a live one on a transient failure would be
// a schema change made for the wrong reason.
func collection(app core.App, name string) (*core.Collection, bool, error) {
	c, err := app.FindCollectionByNameOrId(name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.NewBaseCollection(name), true, nil
	case err != nil:
		return nil, false, fmt.Errorf("edastore: look up collection %q: %w", name, err)
	}
	return c, false, nil
}

func ensureCaptures(app core.App) error {
	c, fresh, err := collection(app, CollCaptures)
	if err != nil || !fresh {
		return err
	}
	c.Fields.Add(
		&core.TextField{Name: "source", Required: true},
		&core.TextField{Name: "remote_addr"},
		&core.TextField{Name: "content_type"},
		// Headers and body are stored verbatim. Field mapping is designed
		// against the real payload, so nothing here may be normalised away.
		&core.JSONField{Name: "headers", MaxSize: 1 << 20},
		&core.TextField{Name: "body", Max: 1 << 20},
		&core.BoolField{Name: "authenticated"},
		&core.AutodateField{Name: "received_at", OnCreate: true},
	)
	c.AddIndex("idx_captures_source", false, "source, received_at", "")
	return save(app, c)
}

func ensureMappings(app core.App) error {
	c, fresh, err := collection(app, CollMappings)
	if err != nil || !fresh {
		return err
	}
	c.Fields.Add(
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "source_hint"},
		&core.JSONField{Name: "fields", MaxSize: 1 << 18},
		&core.AutodateField{Name: "created_at", OnCreate: true},
		&core.AutodateField{Name: "updated_at", OnCreate: true, OnUpdate: true},
	)
	return save(app, c)
}

func ensureBindings(app core.App) error {
	c, fresh, err := collection(app, CollBindings)
	if err != nil || !fresh {
		return err
	}
	mappings, err := app.FindCollectionByNameOrId(CollMappings)
	if err != nil {
		return fmt.Errorf("edastore: bindings need mappings: %w", err)
	}
	c.Fields.Add(
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "source", Required: true},
		&core.RelationField{Name: "mapping", CollectionId: mappings.Id, MaxSelect: 1},
		&core.TextField{Name: "workflow"},
		&core.TextField{Name: "owner"},
		&core.TextField{Name: "repo"},
		&core.NumberField{Name: "version"},
		// status carries the approval gate. It is deliberately not defaulted
		// to anything live: a binding reaches "approved" only by ApproveBinding.
		&core.SelectField{Name: "status", Values: []string{StatusDraft, StatusApproved}, MaxSelect: 1},
		&core.TextField{Name: "secret"},
		&core.AutodateField{Name: "created_at", OnCreate: true},
		&core.AutodateField{Name: "updated_at", OnCreate: true, OnUpdate: true},
	)
	c.AddIndex("idx_bindings_source_status", false, "source, status", "")
	return save(app, c)
}

func ensureBindingDispatches(app core.App) error {
	c, fresh, err := collection(app, CollBindingDispatches)
	if err != nil || !fresh {
		return err
	}
	c.Fields.Add(
		&core.TextField{Name: "binding", Required: true},
		&core.NumberField{Name: "binding_version"},
		&core.TextField{Name: "capture", Required: true},
		// task_id is the seam to the SQLite-owned task tables. It stays a
		// plain integer id rather than a relation precisely because the task
		// lifecycle is not migrating.
		&core.NumberField{Name: "task_id"},
		&core.AutodateField{Name: "dispatched_at", OnCreate: true},
	)
	// At-most-once, enforced by the schema rather than by every caller
	// remembering INSERT OR IGNORE.
	c.AddIndex("idx_binding_dispatch_once", true, "binding, capture", "")
	return save(app, c)
}

func ensurePlaybookDispatches(app core.App) error {
	c, fresh, err := collection(app, CollPlaybookDispatches)
	if err != nil || !fresh {
		return err
	}
	c.Fields.Add(
		&core.TextField{Name: "playbook_id", Required: true},
		&core.TextField{Name: "playbook_version", Required: true},
		&core.TextField{Name: "event_id", Required: true},
		&core.TextField{Name: "action_id", Required: true},
		&core.AutodateField{Name: "dispatched_at", OnCreate: true},
	)
	c.AddIndex("idx_playbook_dispatch_once", true,
		"playbook_id, playbook_version, event_id, action_id", "")
	return save(app, c)
}

// ensureToolCalls adds persistence that did not exist before. Tool calls were
// only ever written to the on-disk task log (internal/logging), so they could
// be read back as text but never queried, filtered or watched. As a collection
// they become all three.
func ensureToolCalls(app core.App) error {
	c, fresh, err := collection(app, CollToolCalls)
	if err != nil || !fresh {
		return err
	}
	c.Fields.Add(
		&core.NumberField{Name: "task_id"},
		&core.NumberField{Name: "attempt"},
		&core.TextField{Name: "tool", Required: true},
		&core.JSONField{Name: "args", MaxSize: 1 << 18},
		&core.JSONField{Name: "result", MaxSize: 1 << 18},
		&core.TextField{Name: "error"},
		&core.NumberField{Name: "duration_ms"},
		&core.AutodateField{Name: "called_at", OnCreate: true},
	)
	c.AddIndex("idx_tool_calls_task", false, "task_id, attempt", "")
	return save(app, c)
}

func save(app core.App, c *core.Collection) error {
	if err := app.Save(c); err != nil {
		return fmt.Errorf("edastore: create collection %q: %w", c.Name, err)
	}
	return nil
}
