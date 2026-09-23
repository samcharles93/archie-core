// Package postgres opens archie's PostgreSQL database and applies its schema
// migrations. This file is the PostgreSQL implementation of the EDA
// persistence contracts (captures, mappings, bindings and the two dispatch
// ledgers) plus the tool_calls transcript, mirroring the PocketBase-backed
// internal/infrastructure/edastore.Store it will replace in the cutover wave.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// EDA is the PostgreSQL implementation of every event-capture contract, plus
// the tool_calls transcript the composition projection writes through. It
// satisfies the same surface as *edastore.Store, so the cutover wave can swap
// one for the other behind storecontract without touching the consumers.
var (
	_ storecontract.CaptureStore       = (*EDA)(nil)
	_ storecontract.MappingStore       = (*EDA)(nil)
	_ storecontract.BindingStore       = (*EDA)(nil)
	_ storecontract.BindingDispatcher  = (*EDA)(nil)
	_ storecontract.PlaybookDispatcher = (*EDA)(nil)
)

// EDA is the PostgreSQL-backed EDA persistence.
type EDA struct {
	pool   *pgxpool.Pool
	q      *postgresdb.Queries
	cipher edastore.BindingCipher
	notify func(events.Event)
}

// NewEDA builds an EDA store over pool. A nil cipher keeps binding secrets in
// plaintext (the behaviour that predates the option); the caller resolves the
// keyring through edastore.NewBindingCipher, exactly as the PocketBase store
// does. The pool is owned by the caller, not this store.
func NewEDA(pool *pgxpool.Pool, cipher edastore.BindingCipher) *EDA {
	return &EDA{pool: pool, q: postgresdb.New(pool), cipher: cipher}
}

// SetNotify wires the change-event callback that the PocketBase store's
// Config.Notify provides. It is the same injection point: nil is a silent
// no-op, which is what every production construction gets today.
func (s *EDA) SetNotify(fn func(events.Event)) { s.notify = fn }

// notifyWrite announces one successful write on the bindings or mappings
// tables. It mirrors edastore.(*Store).notifyWrite byte-for-byte: the data
// carries the record id and action, and consumers refetch the row themselves.
func (s *EDA) notifyWrite(kind, subject, action, id string) {
	if s.notify == nil {
		return
	}
	past := map[string]string{"create": "created", "update": "updated", "approve": "approved", "delete": "deleted"}[action]
	s.notify(events.Event{
		Kind:   kind,
		Detail: subject + " " + past,
		Data:   map[string]any{"id": id, "action": action},
	})
}

func newRecordID() string { return uuid.NewString() }

// --- captures ---

// InsertCapture stores one inbound event verbatim. retention and maxEvents are
// accepted for contract compatibility and applied as a prune after the insert,
// exactly as the PocketBase store does.
func (s *EDA) InsertCapture(ctx context.Context, c storecontract.CapturedEvent, retention time.Duration, maxEvents int) (string, error) {
	received := c.ReceivedAt
	if received.IsZero() {
		received = time.Now()
	}
	id := newRecordID()
	if err := s.q.InsertCapture(ctx, postgresdb.InsertCaptureParams{
		ID:            id,
		Source:        c.Source,
		RemoteAddr:    c.RemoteAddr,
		ContentType:   c.ContentType,
		Headers:       c.Headers,
		Body:          c.Body,
		Authenticated: c.Authenticated,
		ReceivedAt:    received.UTC(),
	}); err != nil {
		return "", fmt.Errorf("edastore: insert capture: %w", err)
	}
	if err := s.pruneCaptures(ctx, retention, maxEvents); err != nil {
		return "", err
	}
	return id, nil
}

func (s *EDA) pruneCaptures(ctx context.Context, retention time.Duration, maxEvents int) error {
	if retention > 0 {
		if err := s.q.DeleteCapturesOlderThan(ctx, time.Now().Add(-retention).UTC()); err != nil {
			return fmt.Errorf("edastore: prune captures by age: %w", err)
		}
	}
	if maxEvents > 0 {
		if err := s.q.DeleteCapturesBeyondCount(ctx, int32(maxEvents)); err != nil {
			return fmt.Errorf("edastore: prune captures by count: %w", err)
		}
	}
	return nil
}

func captureValue(r postgresdb.Capture) storecontract.CapturedEvent {
	return storecontract.CapturedEvent{
		ID:            r.ID,
		ReceivedAt:    r.ReceivedAt,
		Source:        r.Source,
		RemoteAddr:    r.RemoteAddr,
		ContentType:   r.ContentType,
		Headers:       r.Headers,
		Body:          r.Body,
		Authenticated: r.Authenticated,
	}
}

// ListCaptures returns the newest captures first.
func (s *EDA) ListCaptures(ctx context.Context, limit int) ([]storecontract.CapturedEvent, error) {
	rows, err := s.q.ListCaptures(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("edastore: list captures: %w", err)
	}
	out := make([]storecontract.CapturedEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, captureValue(r))
	}
	return out, nil
}

// ListUndispatchedCaptures returns recent captures for the given sources that
// no binding has dispatched yet.
func (s *EDA) ListUndispatchedCaptures(ctx context.Context, sources []string, limit int) ([]storecontract.CapturedEvent, error) {
	if len(sources) == 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := s.q.ListUndispatchedCaptures(ctx, postgresdb.ListUndispatchedCapturesParams{
		Sources:    sources,
		EntryLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("edastore: list undispatched captures: %w", err)
	}
	out := make([]storecontract.CapturedEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, captureValue(r))
	}
	return out, nil
}

// --- mappings ---

func marshalMappingFields(fields []mapping.Field) (string, error) {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("edastore: encode mapping fields: %w", err)
	}
	return string(encoded), nil
}

func mappingValue(r postgresdb.Mapping) (mapping.Mapping, error) {
	var fields []mapping.Field
	if raw := r.Fields; raw != "" {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return mapping.Mapping{}, fmt.Errorf("edastore: decode mapping fields: %w", err)
		}
	}
	return mapping.Mapping{
		ID:         r.ID,
		Name:       r.Name,
		SourceHint: r.SourceHint,
		Fields:     fields,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}, nil
}

func (s *EDA) InsertMapping(ctx context.Context, m mapping.Mapping) (string, error) {
	encoded, err := marshalMappingFields(m.Fields)
	if err != nil {
		return "", err
	}
	id := newRecordID()
	if err := s.q.InsertMapping(ctx, postgresdb.InsertMappingParams{
		ID:         id,
		Name:       m.Name,
		SourceHint: m.SourceHint,
		Fields:     encoded,
	}); err != nil {
		return "", fmt.Errorf("edastore: insert mapping: %w", err)
	}
	s.notifyWrite(events.KindMappingChanged, "mapping", "create", id)
	return id, nil
}

// GetMapping returns (nil, nil) for an absent mapping, matching the
// PocketBase store's found=false convention the gRPC layer translates.
func (s *EDA) GetMapping(ctx context.Context, id string) (*mapping.Mapping, error) {
	r, err := s.q.GetMapping(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := mappingValue(r)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *EDA) ListMappings(ctx context.Context) ([]mapping.Mapping, error) {
	rows, err := s.q.ListMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("edastore: list mappings: %w", err)
	}
	out := make([]mapping.Mapping, 0, len(rows))
	for _, r := range rows {
		m, err := mappingValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *EDA) UpdateMapping(ctx context.Context, m mapping.Mapping) error {
	encoded, err := marshalMappingFields(m.Fields)
	if err != nil {
		return err
	}
	n, err := s.q.UpdateMapping(ctx, postgresdb.UpdateMappingParams{
		ID:         m.ID,
		Name:       m.Name,
		SourceHint: m.SourceHint,
		Fields:     encoded,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrMappingNotFound
	}
	s.notifyWrite(events.KindMappingChanged, "mapping", "update", m.ID)
	return nil
}

func (s *EDA) DeleteMapping(ctx context.Context, id string) error {
	n, err := s.q.DeleteMapping(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrMappingNotFound
	}
	s.notifyWrite(events.KindMappingChanged, "mapping", "delete", id)
	return nil
}

// --- bindings ---

func (s *EDA) encryptSecret(secret string) (string, error) {
	if s.cipher == nil {
		return secret, nil
	}
	encrypted, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("edastore: encrypt binding secret: %w", err)
	}
	return encrypted, nil
}

func (s *EDA) bindingValue(r postgresdb.Binding) (binding.Binding, error) {
	b := binding.Binding{
		ID:        r.ID,
		Name:      r.Name,
		Matcher:   binding.Matcher{Source: r.Source},
		MappingID: r.Mapping,
		Workflow:  r.Workflow,
		Owner:     r.Owner,
		Repo:      r.Repo,
		Version:   int(r.Version),
		Status:    binding.Status(r.Status),
		Secret:    r.Secret,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
	if s.cipher != nil && b.Secret != "" {
		plain, err := s.cipher.Decrypt(b.Secret)
		if err != nil {
			return binding.Binding{}, fmt.Errorf("edastore: decrypt binding secret: %w", err)
		}
		b.Secret = plain
	}
	return b, nil
}

// InsertBinding stores a new binding as pending_approval. "One binding per
// source" is a schema constraint (idx_bindings_source), so a concurrent
// second insert for the same source is refused by the database and surfaced
// here as ErrBindingOverlap.
func (s *EDA) InsertBinding(ctx context.Context, b binding.Binding) (string, error) {
	secret := b.Secret
	if secret != "" {
		var err error
		secret, err = s.encryptSecret(secret)
		if err != nil {
			return "", err
		}
	}
	id := newRecordID()
	err := s.q.InsertBinding(ctx, postgresdb.InsertBindingParams{
		ID:       id,
		Name:     b.Name,
		Source:   b.Matcher.Source,
		Mapping:  b.MappingID,
		Workflow: b.Workflow,
		Owner:    b.Owner,
		Repo:     b.Repo,
		Status:   string(binding.StatusPendingApproval),
		Secret:   secret,
	})
	if isUniqueViolation(err) {
		return "", storecontract.ErrBindingOverlap
	}
	if err != nil {
		return "", fmt.Errorf("edastore: insert binding: %w", err)
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "create", id)
	return id, nil
}

// GetBinding returns (nil, nil) for an absent binding, matching the
// PocketBase store's found=false convention the gRPC layer translates.
func (s *EDA) GetBinding(ctx context.Context, id string) (*binding.Binding, error) {
	r, err := s.q.GetBinding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := s.bindingValue(r)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *EDA) ListBindings(ctx context.Context) ([]binding.Binding, error) {
	rows, err := s.q.ListBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("edastore: list bindings: %w", err)
	}
	out := make([]binding.Binding, 0, len(rows))
	for _, r := range rows {
		b, err := s.bindingValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// ArmedBindingsForSource returns the armed bindings a source can dispatch.
func (s *EDA) ArmedBindingsForSource(ctx context.Context, source string) ([]binding.Binding, error) {
	rows, err := s.q.ArmedBindingsForSource(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("edastore: list armed bindings: %w", err)
	}
	out := make([]binding.Binding, 0, len(rows))
	for _, r := range rows {
		b, err := s.bindingValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// UpdateBinding rewrites a binding's editable fields, bumps its version and
// drops it back to pending_approval. An empty secret means "keep the stored
// one" (the CASE in the query), so an edit form that does not echo the secret
// cannot blank an armed binding's HMAC key.
func (s *EDA) UpdateBinding(ctx context.Context, b binding.Binding) error {
	secret := b.Secret
	if secret != "" {
		var err error
		secret, err = s.encryptSecret(secret)
		if err != nil {
			return err
		}
	}
	n, err := s.q.UpdateBinding(ctx, postgresdb.UpdateBindingParams{
		ID:       b.ID,
		Name:     b.Name,
		Source:   b.Matcher.Source,
		Mapping:  b.MappingID,
		Workflow: b.Workflow,
		Owner:    b.Owner,
		Repo:     b.Repo,
		Status:   string(binding.StatusPendingApproval),
		Secret:   secret,
	})
	if isUniqueViolation(err) {
		return storecontract.ErrBindingOverlap
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrBindingNotFound
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "update", b.ID)
	return nil
}

func (s *EDA) DeleteBinding(ctx context.Context, id string) error {
	n, err := s.q.DeleteBinding(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrBindingNotFound
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "delete", id)
	return nil
}

// ApproveBinding is the only transition that arms a binding. "One armed
// binding per source" is a partial unique constraint (idx_bindings_armed_source),
// so a concurrent approval of a sibling binding on the same source is refused
// by the database and surfaced here as ErrBindingOverlap.
func (s *EDA) ApproveBinding(ctx context.Context, id string) error {
	r, err := s.q.GetBinding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.ErrBindingNotFound
	}
	if err != nil {
		return err
	}
	if r.Status != string(binding.StatusPendingApproval) {
		return storecontract.ErrBindingTransition
	}
	n, err := s.q.SetBindingArmed(ctx, id)
	if isUniqueViolation(err) {
		return storecontract.ErrBindingOverlap
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrBindingNotFound
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "approve", id)
	return nil
}

// --- dispatch ledgers ---

// RecordDispatch writes one at-most-once binding dispatch. binding_version is
// stored but is not part of the unique key, so a version bump does not permit
// re-dispatching the same (binding, capture).
func (s *EDA) RecordDispatch(ctx context.Context, bindingID string, bindingVersion int64, captureID string, taskID int64) error {
	err := s.q.InsertBindingDispatch(ctx, postgresdb.InsertBindingDispatchParams{
		Binding:        bindingID,
		BindingVersion: bindingVersion,
		Capture:        captureID,
		TaskID:         taskID,
	})
	return ledgerWrite(err, "edastore: record dispatch")
}

func (s *EDA) RecordPlaybookDispatch(ctx context.Context, playbookID, playbookVersion, eventID, actionID string) error {
	err := s.q.InsertPlaybookDispatch(ctx, postgresdb.InsertPlaybookDispatchParams{
		PlaybookID:      playbookID,
		PlaybookVersion: playbookVersion,
		EventID:         eventID,
		ActionID:        actionID,
	})
	return ledgerWrite(err, "edastore: record playbook dispatch")
}

func (s *EDA) DeletePlaybookDispatches(ctx context.Context, playbookID string) error {
	if err := s.q.DeletePlaybookDispatches(ctx, playbookID); err != nil {
		return fmt.Errorf("edastore: delete playbook dispatches: %w", err)
	}
	return nil
}

// ledgerWrite turns the unique-index refusal into ErrAlreadyDispatched and
// leaves every other failure alone.
func ledgerWrite(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err):
		return storecontract.ErrAlreadyDispatched
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}

// --- tool calls ---

// InsertToolCall appends one row to the tool_calls transcript.
func (s *EDA) InsertToolCall(ctx context.Context, tc edastore.ToolCall) error {
	calledAt := tc.CalledAt
	if calledAt.IsZero() {
		calledAt = time.Now()
	}
	if err := s.q.InsertToolCall(ctx, postgresdb.InsertToolCallParams{
		ID:       newRecordID(),
		TaskID:   tc.TaskID,
		Attempt:  int64(tc.Attempt),
		Tool:     tc.Tool,
		Result:   tc.Result,
		Error:    tc.Error,
		CalledAt: calledAt,
	}); err != nil {
		return fmt.Errorf("edastore: insert tool call: %w", err)
	}
	return nil
}

// TaskToolCalls returns one task's tool calls in call order.
func (s *EDA) TaskToolCalls(ctx context.Context, taskID int64) ([]edastore.ToolCall, error) {
	rows, err := s.q.TaskToolCalls(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("edastore: list tool calls: %w", err)
	}
	out := make([]edastore.ToolCall, 0, len(rows))
	for _, r := range rows {
		out = append(out, edastore.ToolCall{
			ID:       r.ID,
			TaskID:   r.TaskID,
			Attempt:  int(r.Attempt),
			Tool:     r.Tool,
			Result:   r.Result,
			Error:    r.Error,
			CalledAt: r.CalledAt,
		})
	}
	return out, nil
}
