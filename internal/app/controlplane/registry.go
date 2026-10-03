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
	// Document is a zero value of the resource's document type. The descriptor's
	// JSON Schema is derived from it (see schemaJSON) rather than written out
	// here, so the schema cannot drift from the document it describes. Nil means
	// the definition names no document, which is why bareObjectSchema still
	// exists as a fallback.
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

// The audit identity every seed writes under. It is a constant rather than a
// literal at each call site because a later boot reads it back: it is how a
// stored value is told apart from one an operator replaced (seedWroteNewest),
// and a writer whose identity its own reader cannot name cannot make that
// distinction at all.
const (
	seedActor  = "system:migration"
	seedSource = "legacy-config"
)

// ImportConfig brings every control-plane resource up to date with the value
// this build seeds for it, and reports the version each kind is left at.
//
// For a kind the store does not hold, that is the seed itself. For a kind it
// does hold, the stored value wins and the seed writes nothing -- with one
// exception, because the two ways a kind gets its content are not symmetric:
//
//   - A kind derived from the file config is seeded once and then owned by the
//     store. Editing config.toml does not reach a database that holds a value
//     for it, which is the whole point of the file being only a seed.
//   - A kind whose document the build ships (the ones that set Definition.Defaults,
//     the same set the catalog offers as "restore shipped") is not derived from
//     operator input at all, so a stored copy is a copy of an older build's
//     document and nothing else. A value like that is brought up to date, or a
//     State Store upgraded under a running deployment keeps serving the previous
//     release's document forever. For workflow-definitions that is not a stale
//     setting but an outage: the collection validates as one document, so a
//     stored step type this build no longer has fails every task's pin, whatever
//     workflow the task names.
//
// A refreshed kind keeps its history: the value it replaced stays a revision in
// resource_history. What is replaced is only the seed's own work -- a
// definition set an operator replaced is theirs and is left alone, on the boot
// that finds it and on every boot after.
//
// A seed the resource's own validator refuses is SKIPPED and returned in
// skipped, not fatal. Nothing is written for that kind, so it stays ABSENT and
// the file document's value stays the one in effect (Client.RuntimeConfig
// leaves an absent kind alone), with the fix still in the file. The seed is
// retried on the next start of this process, so correcting config.toml re-seeds
// it. Fail closed belongs to the process that uses the value:
// boot.runtimeConfig validates the effective document and refuses to start
// archied with a value it cannot run with, which is what keeps an invalid value
// from taking effect silently.
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

// seedKind seeds one kind if the store holds nothing for it, refreshes a
// shipped document the seed itself wrote, and otherwise leaves the stored value
// alone, returning the version the kind is left at. A *seedRefusal means the
// value this build derived is not one the resource's validator accepts, and
// nothing was written.
func (s *Server) seedKind(ctx context.Context, definition Definition, cfg config.Config) (int64, error) {
	stored, storedErr := s.store.Resource(ctx, storecontract.DefaultOrgID, definition.Kind)
	absent := errors.Is(storedErr, storecontract.ErrResourceNotFound)
	if storedErr != nil && !absent {
		return 0, storedErr
	}
	value, err := definition.seededValue(cfg)
	if err != nil {
		// Skipped and reported, not fatal: nothing is written, so the kind
		// stays absent and the file document's value is the one in effect,
		// while the process that would RUN the value still fails closed on
		// it (boot.runtimeConfig validates the effective document).
		// seededValue wraps a seed's encode and validation failures together,
		// which is why both take this path: the alternative is a second seed
		// implementation here to tell them apart.
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

// shippedValueIsStale reports whether a stored value is the build's to replace:
// the kind ships a document of its own (Defaults is set exactly for those, and
// a file-derived seed never overwrites a stored value) and the revision the
// seed itself wrote is the newest one. A newer revision carrying any other
// identity is an operator's replacement, and an operator's replacement is not
// the seed's to undo.
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

// seedRequestID is the idempotency key a seed writes under. It is derived from
// the value rather than from the kind alone, because the key is spent the
// moment it is in the ledger (resource_history's UNIQUE (kind, request_id), and
// PutResource answers a known key from that ledger): a key derived from the
// kind makes every later value a replay of the first one ever written, which is
// how a document that changed in the build never reached a database the seed
// had already written. Deriving it from the value keeps the write idempotent --
// the same document twice is still one revision -- without making the second
// document impossible.
func seedRequestID(kind string, value []byte) string {
	sum := sha256.Sum256(value)
	return "import:" + kind + ":" + hex.EncodeToString(sum[:])
}

// seededValue is the value the State Store writes for a kind it does not hold
// yet: the definition's seed derived from cfg, run through the same Decode a
// write applies. ImportConfig stores it; the offline config layering reads it
// for the same reason, so "what the daemon sees for a kind that was never
// stored" is one value rather than two.
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

// ValidateStored decodes every stored resource with the definition that owns
// it, using the same Decode the write path applies, and reports how many
// resources it checked. Errors name the kind and the revision it was stored at,
// which is what an operator needs to roll the value back.
//
// It is the write path's own check, not boot's: boot refuses on
// configuration.Validate over the stored values layered onto the file config,
// and the two disagree in both directions (this one rejects unknown fields
// boot's decode ignores, while only boot checks dispatch.trigger and a positive
// poll interval). A caller answering "would the daemon start" therefore needs
// StoredRuntimeConfig and configuration.Validate as well -- see
// validateStore in internal/app/archied/state_store_recovery.go.
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
