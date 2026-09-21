package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/store"
)

type Definition struct {
	Kind      string
	Title     string
	Schema    string
	ApplyMode string
	Seed      func(config.Config) any
	Validate  func([]byte) error
	Normalize func([]byte) ([]byte, error)
	// Defaults is the factory value clients offer as "restore shipped". Only
	// resources that ship with content set it.
	Defaults func() any
}

func (d Definition) Descriptor() *pb.ResourceDescriptor {
	descriptor := &pb.ResourceDescriptor{Kind: d.Kind, Title: d.Title, SchemaJson: d.Schema, Commands: []string{"replace"}, ApplyMode: d.ApplyMode}
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

// ImportConfig seeds every control-plane resource that has no stored value yet
// from the file config, and reports the version of every kind it left alone.
//
// A seed the resource's own validator refuses is SKIPPED and returned in skipped,
// not fatal. The setting it would have carried stays file-owned, so the value in
// effect is still the file's, and the fix is still the file's.
// docs/prds/runtime-control-plane.md, "Bootstrap, migration, and recovery":
// after migration, settings in TOML are ignored and cannot block State Store
// startup -- and this import runs on the State Store's startup path. Fail closed
// belongs to the process that uses the value: boot.runtimeConfig validates the
// effective document and refuses to start archied with a value it cannot run
// with, which is what keeps an invalid value from taking effect silently.
func (s *Server) ImportConfig(ctx context.Context, cfg config.Config) (map[string]int64, []SeedSkip, error) {
	versions := make(map[string]int64, len(s.ordered))
	var skipped []SeedSkip
	for _, definition := range s.ordered {
		resource, err := s.store.Resource(ctx, definition.Kind)
		if err == nil {
			versions[definition.Kind] = resource.Version
			continue
		}
		if !errors.Is(err, store.ErrResourceNotFound) {
			return nil, nil, err
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
			skipped = append(skipped, SeedSkip{Kind: definition.Kind, Err: err})
			continue
		}
		resource, err = s.store.PutResource(ctx, store.ResourceWrite{Kind: definition.Kind, Value: value, Actor: "system:migration", Source: "legacy-config", RequestID: "import:" + definition.Kind, ExpectedVersion: 0, At: time.Now().UTC()})
		if err != nil {
			return nil, nil, fmt.Errorf("seed %s: %w", definition.Kind, err)
		}
		versions[definition.Kind] = resource.Version
	}
	return versions, skipped, nil
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
		resource, err := s.store.Resource(ctx, definition.Kind)
		if errors.Is(err, store.ErrResourceNotFound) {
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
