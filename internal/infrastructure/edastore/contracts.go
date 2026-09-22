package edastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// capturedAtLayout is fixed-width UTC with nanosecond precision, so string
// comparison is time comparison. time.RFC3339Nano trims trailing zeros, which
// would break that.
const capturedAtLayout = "2006-01-02T15:04:05.000000000Z"

// This store is the implementation of every event-capture contract. The task
// lifecycle contracts stay on the SQLite store; the two meet only at
// binding_dispatches.task_id.
var (
	_ storecontract.CaptureStore       = (*Store)(nil)
	_ storecontract.MappingStore       = (*Store)(nil)
	_ storecontract.BindingStore       = (*Store)(nil)
	_ storecontract.BindingDispatcher  = (*Store)(nil)
	_ storecontract.PlaybookDispatcher = (*Store)(nil)
)

func (s *Store) record(collection, id string) (*core.Record, error) {
	r, err := s.app.FindRecordById(collection, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// InsertCapture stores one inbound event verbatim. Redaction happens upstream
// in webhookguard; this persists exactly what it is given, because field
// mapping is designed against the payload as received.
//
// retention and maxEvents are accepted for contract compatibility and applied
// as a prune after the insert.
func (s *Store) InsertCapture(ctx context.Context, c storecontract.CapturedEvent, retention time.Duration, maxEvents int) (string, error) {
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
	received := c.ReceivedAt
	if received.IsZero() {
		received = time.Now()
	}
	r.Set("received_at", received.UTC().Format(capturedAtLayout))
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("edastore: insert capture: %w", err)
	}
	if err := s.pruneCaptures(ctx, retention, maxEvents); err != nil {
		return "", err
	}
	return r.Id, nil
}

// pruneCaptures enforces the retention window and count cap. A capture exists
// to be mapped against while it is recent; keeping every event forever would
// make an unauthenticated intake surface a disk-growth vector.
func (s *Store) pruneCaptures(_ context.Context, retention time.Duration, maxEvents int) error {
	if retention > 0 {
		cutoff := time.Now().Add(-retention).UTC().Format(time.RFC3339)
		if _, err := s.app.DB().NewQuery(
			"DELETE FROM " + CollCaptures + " WHERE received_at < {:cutoff}").
			Bind(map[string]any{"cutoff": cutoff}).Execute(); err != nil {
			return fmt.Errorf("edastore: prune captures by age: %w", err)
		}
	}
	if maxEvents > 0 {
		if _, err := s.app.DB().NewQuery(
			"DELETE FROM " + CollCaptures + " WHERE id NOT IN (SELECT id FROM " + CollCaptures +
				" ORDER BY received_at DESC LIMIT {:keep})").
			Bind(map[string]any{"keep": maxEvents}).Execute(); err != nil {
			return fmt.Errorf("edastore: prune captures by count: %w", err)
		}
	}
	return nil
}

// parseCapturedAt reads the stored timestamp, tolerating the RFC3339 form a
// caller-supplied value may carry.
func parseCapturedAt(raw string) time.Time {
	for _, layout := range []string{capturedAtLayout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func captureValue(r *core.Record) storecontract.CapturedEvent {
	return storecontract.CapturedEvent{
		ID:            r.Id,
		ReceivedAt:    parseCapturedAt(r.GetString("received_at")),
		Source:        r.GetString("source"),
		RemoteAddr:    r.GetString("remote_addr"),
		ContentType:   r.GetString("content_type"),
		Headers:       r.GetString("headers"),
		Body:          r.GetString("body"),
		Authenticated: r.GetBool("authenticated"),
	}
}

// ListCaptures returns the newest captures first.
func (s *Store) ListCaptures(_ context.Context, limit int) ([]storecontract.CapturedEvent, error) {
	records, err := s.app.FindRecordsByFilter(CollCaptures, "", "-received_at", limit, 0)
	if err != nil {
		return nil, fmt.Errorf("edastore: list captures: %w", err)
	}
	out := make([]storecontract.CapturedEvent, 0, len(records))
	for _, r := range records {
		out = append(out, captureValue(r))
	}
	return out, nil
}

// ListUndispatchedCaptures returns recent captures for the given sources that
// no binding has dispatched yet.
//
// This is raw SQL rather than a record filter: the anti-join against the
// dispatch ledger is a subquery, and PocketBase's filter DSL is not SQL (it
// has no IN (SELECT ...) form). A read fires no hooks either way, so nothing
// is lost by dropping to the query builder here.
func (s *Store) ListUndispatchedCaptures(_ context.Context, sources []string, limit int) ([]storecontract.CapturedEvent, error) {
	if len(sources) == 0 || limit <= 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(sources))
	params := dbx.Params{"limit": limit}
	for i, src := range sources {
		key := fmt.Sprintf("s%d", i)
		placeholders = append(placeholders, "{:"+key+"}")
		params[key] = src
	}
	query := "SELECT id, received_at, source, remote_addr, content_type, headers, body, authenticated FROM " +
		CollCaptures + " WHERE source IN (" + strings.Join(placeholders, ",") +
		") AND id NOT IN (SELECT capture FROM " + CollBindingDispatches + ") ORDER BY received_at DESC LIMIT {:limit}"

	var rows []captureRow
	if err := s.app.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
		return nil, fmt.Errorf("edastore: list undispatched captures: %w", err)
	}
	out := make([]storecontract.CapturedEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.value())
	}
	return out, nil
}

// captureRow is the raw-SQL shape of a capture, used only where a record
// filter cannot express the query.
type captureRow struct {
	ID            string `db:"id"`
	ReceivedAt    string `db:"received_at"`
	Source        string `db:"source"`
	RemoteAddr    string `db:"remote_addr"`
	ContentType   string `db:"content_type"`
	Headers       string `db:"headers"`
	Body          string `db:"body"`
	Authenticated bool   `db:"authenticated"`
}

func (r captureRow) value() storecontract.CapturedEvent {
	return storecontract.CapturedEvent{
		ID:            r.ID,
		ReceivedAt:    parseCapturedAt(r.ReceivedAt),
		Source:        r.Source,
		RemoteAddr:    r.RemoteAddr,
		ContentType:   r.ContentType,
		Headers:       r.Headers,
		Body:          r.Body,
		Authenticated: r.Authenticated,
	}
}
