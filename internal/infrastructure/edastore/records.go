package edastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// Capture is one inbound event, stored whether or not anything understands it.
type Capture struct {
	ID            string
	Source        string
	RemoteAddr    string
	ContentType   string
	Headers       string
	Body          string
	Authenticated bool
}

// Binding ties a matcher and mapping to a workflow.
type Binding struct {
	ID        string
	Name      string
	Source    string
	MappingID string
	Workflow  string
	Owner     string
	Repo      string
	Version   int64
	Status    string
	Secret    string
}

// Dispatch is one at-most-once binding dispatch ledger entry.
type Dispatch struct {
	BindingID      string
	BindingVersion int64
	CaptureID      string
	// TaskID addresses the SQLite-owned task tables, which are not migrating.
	TaskID int64
}

// PlaybookDispatch is one at-most-once playbook action ledger entry.
type PlaybookDispatch struct {
	PlaybookID      string
	PlaybookVersion string
	EventID         string
	ActionID        string
}

// InsertCapture stores one inbound event and returns its id.
func (s *Store) InsertCapture(_ context.Context, c Capture) (string, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollCaptures)
	if err != nil {
		return "", err
	}
	r := core.NewRecord(collection)
	r.Set("source", c.Source)
	r.Set("remote_addr", c.RemoteAddr)
	r.Set("content_type", c.ContentType)
	r.Set("headers", c.Headers)
	r.Set("body", c.Body)
	r.Set("authenticated", c.Authenticated)
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("edastore: insert capture: %w", err)
	}
	return r.Id, nil
}

// Capture returns one stored event verbatim.
func (s *Store) Capture(_ context.Context, id string) (Capture, error) {
	r, err := s.app.FindRecordById(CollCaptures, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Capture{}, ErrCaptureNotFound
		}
		return Capture{}, err
	}
	return Capture{
		ID:            r.Id,
		Source:        r.GetString("source"),
		RemoteAddr:    r.GetString("remote_addr"),
		ContentType:   r.GetString("content_type"),
		Headers:       r.GetString("headers"),
		Body:          r.GetString("body"),
		Authenticated: r.GetBool("authenticated"),
	}, nil
}

// InsertBinding stores a new binding. It is always created as a draft: the
// epic requires explicit human approval before a binding can run anything.
func (s *Store) InsertBinding(_ context.Context, b Binding) (string, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollBindings)
	if err != nil {
		return "", err
	}
	r := core.NewRecord(collection)
	r.Set("name", b.Name)
	r.Set("source", b.Source)
	r.Set("mapping", b.MappingID)
	r.Set("workflow", b.Workflow)
	r.Set("owner", b.Owner)
	r.Set("repo", b.Repo)
	r.Set("version", 1)
	r.Set("status", StatusDraft)
	r.Set("secret", b.Secret)
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("edastore: insert binding: %w", err)
	}
	return r.Id, nil
}

// GetBinding returns one binding.
func (s *Store) GetBinding(_ context.Context, id string) (Binding, error) {
	r, err := s.app.FindRecordById(CollBindings, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Binding{}, ErrBindingNotFound
		}
		return Binding{}, err
	}
	return Binding{
		ID:        r.Id,
		Name:      r.GetString("name"),
		Source:    r.GetString("source"),
		MappingID: r.GetString("mapping"),
		Workflow:  r.GetString("workflow"),
		Owner:     r.GetString("owner"),
		Repo:      r.GetString("repo"),
		Version:   int64(r.GetInt("version")),
		Status:    r.GetString("status"),
		Secret:    r.GetString("secret"),
	}, nil
}

// ApproveBinding moves a draft binding to approved. It is the only way a
// binding becomes live.
func (s *Store) ApproveBinding(_ context.Context, id string) error {
	r, err := s.app.FindRecordById(CollBindings, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBindingNotFound
		}
		return err
	}
	r.Set("status", StatusApproved)
	r.Set("version", r.GetInt("version")+1)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("edastore: approve binding: %w", err)
	}
	return nil
}

// RecordDispatch writes one binding dispatch, reporting whether it was new.
// The (binding, capture) uniqueness is enforced by the collection's index, so
// a duplicate is refused by the schema rather than by the caller.
func (s *Store) RecordDispatch(_ context.Context, d Dispatch) (bool, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollBindingDispatches)
	if err != nil {
		return false, err
	}
	r := core.NewRecord(collection)
	r.Set("binding", d.BindingID)
	r.Set("binding_version", d.BindingVersion)
	r.Set("capture", d.CaptureID)
	r.Set("task_id", d.TaskID)
	return savedOrDuplicate(s.app, r, "edastore: record dispatch")
}

// RecordPlaybookDispatch writes one playbook action dispatch, reporting
// whether it was new.
func (s *Store) RecordPlaybookDispatch(_ context.Context, d PlaybookDispatch) (bool, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollPlaybookDispatches)
	if err != nil {
		return false, err
	}
	r := core.NewRecord(collection)
	r.Set("playbook_id", d.PlaybookID)
	r.Set("playbook_version", d.PlaybookVersion)
	r.Set("event_id", d.EventID)
	r.Set("action_id", d.ActionID)
	return savedOrDuplicate(s.app, r, "edastore: record playbook dispatch")
}

// savedOrDuplicate saves a ledger record, translating the unique-index
// violation into "already recorded" rather than an error. Anything else is a
// real failure and is returned: silently swallowing every save error here
// would turn a broken ledger into a silent at-least-once dispatcher.
func savedOrDuplicate(app core.App, r *core.Record, what string) (bool, error) {
	err := app.Save(r)
	switch {
	case err == nil:
		return true, nil
	case isUniqueViolation(err):
		return false, nil
	default:
		return false, fmt.Errorf("%s: %w", what, err)
	}
}

// isUniqueViolation reports whether err is the unique-index refusal. PocketBase
// surfaces it as a validation failure rather than a typed error, so the text is
// what there is to match on.
func isUniqueViolation(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique") || strings.Contains(text, "already exists")
}
