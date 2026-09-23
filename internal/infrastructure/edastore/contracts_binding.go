package edastore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/events"
)

// --- mappings ---

func mappingValue(r *core.Record) (mapping.Mapping, error) {
	var fields []mapping.Field
	if raw := r.GetString("fields"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return mapping.Mapping{}, fmt.Errorf("edastore: decode mapping fields: %w", err)
		}
	}
	return mapping.Mapping{
		ID:         r.Id,
		Name:       r.GetString("name"),
		SourceHint: r.GetString("source_hint"),
		Fields:     fields,
		CreatedAt:  r.GetDateTime("created_at").Time(),
		UpdatedAt:  r.GetDateTime("updated_at").Time(),
	}, nil
}

func (s *Store) applyMapping(r *core.Record, m mapping.Mapping) error {
	encoded, err := json.Marshal(m.Fields)
	if err != nil {
		return fmt.Errorf("edastore: encode mapping fields: %w", err)
	}
	r.Set("name", m.Name)
	r.Set("source_hint", m.SourceHint)
	r.Set("fields", string(encoded))
	return nil
}

func (s *Store) InsertMapping(_ context.Context, m mapping.Mapping) (string, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollMappings)
	if err != nil {
		return "", err
	}
	r := core.NewRecord(collection)
	if err := s.applyMapping(r, m); err != nil {
		return "", err
	}
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("edastore: insert mapping: %w", err)
	}
	s.notifyWrite(events.KindMappingChanged, "mapping", "create", r.Id)
	return r.Id, nil
}

func (s *Store) GetMapping(_ context.Context, id string) (*mapping.Mapping, error) {
	r, err := s.record(CollMappings, id)
	if err != nil || r == nil {
		return nil, err
	}
	m, err := mappingValue(r)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListMappings(_ context.Context) ([]mapping.Mapping, error) {
	records, err := s.app.FindRecordsByFilter(CollMappings, "", "-created_at", 0, 0)
	if err != nil {
		return nil, fmt.Errorf("edastore: list mappings: %w", err)
	}
	out := make([]mapping.Mapping, 0, len(records))
	for _, r := range records {
		m, err := mappingValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) UpdateMapping(_ context.Context, m mapping.Mapping) error {
	r, err := s.record(CollMappings, m.ID)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrMappingNotFound
	}
	if err := s.applyMapping(r, m); err != nil {
		return err
	}
	if err := s.app.Save(r); err != nil {
		return err
	}
	s.notifyWrite(events.KindMappingChanged, "mapping", "update", m.ID)
	return nil
}

func (s *Store) DeleteMapping(_ context.Context, id string) error {
	r, err := s.record(CollMappings, id)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrMappingNotFound
	}
	if err := s.app.Delete(r); err != nil {
		return err
	}
	s.notifyWrite(events.KindMappingChanged, "mapping", "delete", id)
	return nil
}

// --- bindings ---

func (s *Store) bindingValue(r *core.Record) (binding.Binding, error) {
	b := binding.Binding{
		ID:        r.Id,
		Name:      r.GetString("name"),
		Matcher:   binding.Matcher{Source: r.GetString("source")},
		MappingID: r.GetString("mapping"),
		Workflow:  r.GetString("workflow"),
		Owner:     r.GetString("owner"),
		Repo:      r.GetString("repo"),
		Version:   r.GetInt("version"),
		Status:    binding.Status(r.GetString("status")),
		Secret:    r.GetString("secret"),
		CreatedAt: r.GetDateTime("created_at").Time(),
		UpdatedAt: r.GetDateTime("updated_at").Time(),
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

func (s *Store) applyBinding(r *core.Record, b binding.Binding) error {
	r.Set("name", b.Name)
	r.Set("source", b.Matcher.Source)
	r.Set("mapping", b.MappingID)
	r.Set("workflow", b.Workflow)
	r.Set("owner", b.Owner)
	r.Set("repo", b.Repo)
	// An empty secret means "keep the stored one". An edit form that does not
	// echo the secret back must not silently disarm the binding by erasing it.
	if b.Secret == "" {
		return nil
	}
	secret := b.Secret
	if s.cipher != nil {
		encrypted, err := s.cipher.Encrypt(secret)
		if err != nil {
			return fmt.Errorf("edastore: encrypt binding secret: %w", err)
		}
		secret = encrypted
	}
	r.Set("secret", secret)
	return nil
}

// InsertBinding stores a new binding as a draft. The caller's status is
// ignored on purpose: a binding cannot go live without explicit approval, so
// ApproveBinding is the only path to armed.
//
// A source may carry at most one binding, so an insert against a source that
// already has one is refused rather than creating an ambiguous second matcher.
func (s *Store) InsertBinding(ctx context.Context, b binding.Binding) (string, error) {
	existing, err := s.bindingsByFilter("source = {:source}", map[string]any{"source": b.Matcher.Source})
	if err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return "", ErrBindingOverlap
	}
	collection, err := s.app.FindCollectionByNameOrId(CollBindings)
	if err != nil {
		return "", err
	}
	r := core.NewRecord(collection)
	if err := s.applyBinding(r, b); err != nil {
		return "", err
	}
	r.Set("version", 1)
	r.Set("status", string(binding.StatusPendingApproval))
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("edastore: insert binding: %w", err)
	}
	_ = ctx
	s.notifyWrite(events.KindBindingChanged, "binding", "create", r.Id)
	return r.Id, nil
}

func (s *Store) GetBinding(_ context.Context, id string) (*binding.Binding, error) {
	r, err := s.record(CollBindings, id)
	if err != nil || r == nil {
		return nil, err
	}
	b, err := s.bindingValue(r)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) ListBindings(_ context.Context) ([]binding.Binding, error) {
	return s.bindingsByFilter("", nil)
}

// ArmedBindingsForSource returns the armed bindings a source can dispatch. A
// draft or pending_approval binding is never returned: that is the gate.
func (s *Store) ArmedBindingsForSource(_ context.Context, source string) ([]binding.Binding, error) {
	return s.bindingsByFilter("source = {:source} && status = {:status}",
		map[string]any{"source": source, "status": string(binding.StatusArmed)})
}

func (s *Store) bindingsByFilter(filter string, params map[string]any) ([]binding.Binding, error) {
	var records []*core.Record
	var err error
	if params == nil {
		records, err = s.app.FindRecordsByFilter(CollBindings, filter, "-created_at", 0, 0)
	} else {
		records, err = s.app.FindRecordsByFilter(CollBindings, filter, "-created_at", 0, 0, params)
	}
	if err != nil {
		return nil, fmt.Errorf("edastore: list bindings: %w", err)
	}
	out := make([]binding.Binding, 0, len(records))
	for _, r := range records {
		b, err := s.bindingValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// UpdateBinding rewrites a binding's editable fields, bumps its version, and
// sends it back to pending_approval. An edit always requires re-approval: an
// armed binding whose matcher or workflow changed is a different binding, and
// re-arming it silently would defeat the gate.
func (s *Store) UpdateBinding(_ context.Context, b binding.Binding) error {
	r, err := s.record(CollBindings, b.ID)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrBindingNotFound
	}
	others, err := s.bindingsByFilter("source = {:source} && id != {:id}",
		map[string]any{"source": b.Matcher.Source, "id": b.ID})
	if err != nil {
		return err
	}
	if len(others) > 0 {
		return ErrBindingOverlap
	}
	if err := s.applyBinding(r, b); err != nil {
		return err
	}
	r.Set("status", string(binding.StatusPendingApproval))
	r.Set("version", r.GetInt("version")+1)
	if err := s.app.Save(r); err != nil {
		return err
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "update", b.ID)
	return nil
}

func (s *Store) DeleteBinding(_ context.Context, id string) error {
	r, err := s.record(CollBindings, id)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrBindingNotFound
	}
	if err := s.app.Delete(r); err != nil {
		return err
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "delete", id)
	return nil
}

// ApproveBinding is the only transition that arms a binding. It moves
// pending_approval to armed; any other current state is refused, so a draft
// cannot skip review and an armed binding cannot be re-approved.
//
// A source may have only one armed binding, so approval is refused when
// another binding on the same source is already armed. Without that guard,
// one capture could match two armed matchers and dispatch twice.
func (s *Store) ApproveBinding(_ context.Context, id string) error {
	r, err := s.record(CollBindings, id)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrBindingNotFound
	}
	armed, err := s.bindingsByFilter("source = {:source} && status = {:status} && id != {:id}",
		map[string]any{"source": r.GetString("source"), "status": string(binding.StatusArmed), "id": id})
	if err != nil {
		return err
	}
	if len(armed) > 0 {
		return ErrBindingOverlap
	}
	if r.GetString("status") != string(binding.StatusPendingApproval) {
		return ErrBindingTransition
	}
	r.Set("status", string(binding.StatusArmed))
	if err := s.app.Save(r); err != nil {
		return err
	}
	s.notifyWrite(events.KindBindingChanged, "binding", "approve", id)
	return nil
}

// --- dispatch ledgers ---

// RecordDispatch writes one at-most-once binding dispatch. Uniqueness is the
// collection's index, so a replay is refused by the schema; the caller sees
// ErrAlreadyDispatched rather than a silent second run.
func (s *Store) RecordDispatch(_ context.Context, bindingID string, bindingVersion int64, captureID string, taskID int64) error {
	collection, err := s.app.FindCollectionByNameOrId(CollBindingDispatches)
	if err != nil {
		return err
	}
	r := core.NewRecord(collection)
	r.Set("binding", bindingID)
	r.Set("binding_version", bindingVersion)
	r.Set("capture", captureID)
	r.Set("task_id", taskID)
	return ledgerWrite(s.app.Save(r), "edastore: record dispatch")
}

func (s *Store) RecordPlaybookDispatch(_ context.Context, playbookID, playbookVersion, eventID, actionID string) error {
	collection, err := s.app.FindCollectionByNameOrId(CollPlaybookDispatches)
	if err != nil {
		return err
	}
	r := core.NewRecord(collection)
	r.Set("playbook_id", playbookID)
	r.Set("playbook_version", playbookVersion)
	r.Set("event_id", eventID)
	r.Set("action_id", actionID)
	return ledgerWrite(s.app.Save(r), "edastore: record playbook dispatch")
}

func (s *Store) DeletePlaybookDispatches(_ context.Context, playbookID string) error {
	_, err := s.app.DB().NewQuery(
		"DELETE FROM " + CollPlaybookDispatches + " WHERE playbook_id = {:id}").
		Bind(map[string]any{"id": playbookID}).Execute()
	if err != nil {
		return fmt.Errorf("edastore: delete playbook dispatches: %w", err)
	}
	return nil
}

// ledgerWrite turns the unique-index refusal into ErrAlreadyDispatched and
// leaves every other failure alone. Treating all errors as "already recorded"
// would turn an at-most-once ledger into a silent at-least-once dispatcher.
func ledgerWrite(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err):
		return ErrAlreadyDispatched
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}

// isUniqueViolation reports whether err is the unique-index refusal.
// PocketBase surfaces it as a validation failure rather than a typed error,
// so the message is what there is to match on.
func isUniqueViolation(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique") || strings.Contains(text, "already exists")
}
