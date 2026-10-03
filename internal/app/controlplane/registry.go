package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type Definition struct {
	Kind  string
	Title string
	// Document is a zero value of the resource's document type; the JSON Schema
	// is derived from it. Nil means no document.
	Document  any
	ApplyMode string
	Seed      func(config.Config) any
	Validate  func([]byte) error
	Normalize func([]byte) ([]byte, error)
	// Defaults is the factory value clients offer as "restore shipped". Only
	// resources that ship with content set it.
	Defaults func() any
}

func (d Definition) Descriptor() *pb.ResourceDescriptor {
	descriptor := &pb.ResourceDescriptor{Kind: d.Kind, Title: d.Title, SchemaJson: schemaJSON(d.Document), Commands: []string{"replace"}, ApplyMode: d.ApplyMode}
	if d.Defaults != nil {
		if defaults, err := json.Marshal(d.Defaults()); err == nil {
			descriptor.DefaultsJson = string(defaults)
		}
	}
	return descriptor
}

func (d Definition) Decode(input []byte) ([]byte, error) {
	if !json.Valid(input) {
		return nil, fmt.Errorf("%w: invalid JSON", ErrValidation)
	}
	if err := d.Validate(input); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	if d.Normalize != nil {
		normalized, err := d.Normalize(input)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrValidation, err)
		}
		input = normalized
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", d.Kind, err)
	}
	return encoded, nil
}

// SeedSkip records a resource ImportConfig could not seed from the file config,
// and why. It is reported rather than returned as an error: see ImportConfig.
type SeedSkip struct {
	Kind string
	Err  error
}

// seedActor is the audit identity seeds write under.
const (
	seedActor  = "system:migration"
	seedSource = "legacy-config"
)

// ImportConfig seeds every resource the store does not hold and returns the
// version each kind is left at. A stored value is kept, except a shipped
// document the seed itself wrote, which is refreshed. A seed its validator
// refuses is skipped and returned in skipped.
func (s *Server) ImportConfig(ctx context.Context, cfg config.Config) (map[string]int64, []SeedSkip, error) {
	versions := make(map[string]int64, len(s.ordered))
	var skipped []SeedSkip
	for _, definition := range s.ordered {
		version, err := s.seedKind(ctx, definition, cfg)
		var refusal *seedRefusal
		switch {
		case errors.As(err, &refusal):
			skipped = append(skipped, SeedSkip{Kind: definition.Kind, Err: refusal.err})
		case err != nil:
			return nil, nil, err
		default:
			versions[definition.Kind] = version
		}
	}
	return versions, skipped, nil
}

// seedRefusal is the seed's own validation refusing the value this build
// derived. It is an error because it is one -- there is nothing to write -- and a
// distinct type because ImportConfig answers it by reporting the kind rather
// than failing the boot (see ImportConfig on why a bad seed is not fatal).
type seedRefusal struct{ err error }

func (r *seedRefusal) Error() string { return r.err.Error() }

func (r *seedRefusal) Unwrap() error { return r.err }

// seedKind seeds, refreshes or leaves one kind and returns its version. A
// *seedRefusal means the seed failed validation and nothing was written.
func (s *Server) seedKind(ctx context.Context, definition Definition, cfg config.Config) (int64, error) {
	stored, storedErr := s.store.Resource(ctx, storecontract.DefaultOrgID, definition.Kind)
	absent := errors.Is(storedErr, storecontract.ErrResourceNotFound)
	if storedErr != nil && !absent {
		return 0, storedErr
	}
	value, err := definition.seededValue(cfg)
	if err != nil {
		// Skipped, not fatal.
		return 0, &seedRefusal{err: err}
	}
	if !absent && bytes.Equal(stored.Value, value) {
		// The store already holds what this build seeds. Comparing the values
		// rather than re-writing them is what keeps a boot from adding a
		// revision for a document that did not change.
		return stored.Version, nil
	}
	if !absent {
		refresh, err := s.shippedValueIsStale(ctx, definition)
		if err != nil {
			return 0, err
		}
		if !refresh {
			return stored.Version, nil
		}
	}
	write := storecontract.ResourceWrite{
		OrgID: storecontract.DefaultOrgID, Kind: definition.Kind, Value: value,
		Actor: seedActor, Source: seedSource,
		RequestID:       seedRequestID(definition.Kind, value),
		ExpectedVersion: expectedVersion(stored, absent), At: time.Now().UTC(),
	}
	resource, err := s.store.PutResource(ctx, write)
	if err != nil {
		return 0, fmt.Errorf("seed %s: %w", definition.Kind, err)
	}
	return resource.Version, nil
}

// expectedVersion is the optimistic-concurrency guard a seed writes under: the
// revision it is replacing, or nothing at all for a kind that has none.
func expectedVersion(stored storecontract.Resource, absent bool) int64 {
	if absent {
		return 0
	}
	return stored.Version
}

// shippedValueIsStale reports whether the newest stored revision of a
// shipped document was written by the seed.
func (s *Server) shippedValueIsStale(ctx context.Context, definition Definition) (bool, error) {
	if definition.Defaults == nil {
		return false, nil
	}
	return s.seedWroteNewest(ctx, definition.Kind)
}

// seedWroteNewest reports whether the newest revision of kind was written by a
// seed. It reads the ledger rather than the current row because the ledger is
// what records who wrote a value, and the row does not.
func (s *Server) seedWroteNewest(ctx context.Context, kind string) (bool, error) {
	history, err := s.store.ResourceHistory(ctx, storecontract.DefaultOrgID, kind, 1)
	if err != nil {
		return false, fmt.Errorf("read %s history: %w", kind, err)
	}
	if len(history) == 0 {
		return false, nil
	}
	return history[0].Actor == seedActor && history[0].Source == seedSource, nil
}

// seedRequestID is a seed write's idempotency key, derived from the kind and
// value.
func seedRequestID(kind string, value []byte) string {
	sum := sha256.Sum256(value)
	return "import:" + kind + ":" + hex.EncodeToString(sum[:])
}

// seededValue is the value written for a kind the store does not hold.
func (d Definition) seededValue(cfg config.Config) ([]byte, error) {
	value, err := json.Marshal(d.Seed(cfg))
	if err != nil {
		return nil, fmt.Errorf("seed %s: %w", d.Kind, err)
	}
	value, err = d.Decode(value)
	if err != nil {
		return nil, fmt.Errorf("seed %s: %w", d.Kind, err)
	}
	return value, nil
}

// seededValues is what the State Store would write for every kind, keyed by
// kind.
func (s *Server) seededValues(cfg config.Config) (map[string][]byte, error) {
	values := make(map[string][]byte, len(s.ordered))
	for _, definition := range s.ordered {
		value, err := definition.seededValue(cfg)
		if err != nil {
			return nil, err
		}
		values[definition.Kind] = value
	}
	return values, nil
}

// Owns reports whether kind is a resource the control plane owns, so a caller
// acting on a stored value can refuse a kind nobody defines before it reaches
// the store.
func (s *Server) Owns(kind string) bool {
	_, ok := s.definitions[kind]
	return ok
}

// ValidateStored decodes every stored resource with its definition and
// returns how many it checked. Errors name the kind and revision.
func (s *Server) ValidateStored(ctx context.Context) (int, error) {
	checked := 0
	var failures []error
	for _, definition := range s.ordered {
		resource, err := s.store.Resource(ctx, storecontract.DefaultOrgID, definition.Kind)
		if errors.Is(err, storecontract.ErrResourceNotFound) {
			continue
		}
		if err != nil {
			return checked, fmt.Errorf("read %s: %w", definition.Kind, err)
		}
		checked++
		if _, err := definition.Decode(resource.Value); err != nil {
			failures = append(failures, fmt.Errorf("%s at version %d: %w", definition.Kind, resource.Version, err))
		}
	}
	return checked, errors.Join(failures...)
}

func validateAs[T any](input []byte, validate func(T) error) error {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return validate(value)
}
