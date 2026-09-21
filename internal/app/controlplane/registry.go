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

func (s *Server) ImportConfig(ctx context.Context, cfg config.Config) (map[string]int64, error) {
	versions := make(map[string]int64, len(s.ordered))
	for _, definition := range s.ordered {
		resource, err := s.store.Resource(ctx, definition.Kind)
		if err == nil {
			versions[definition.Kind] = resource.Version
			continue
		}
		if !errors.Is(err, store.ErrResourceNotFound) {
			return nil, err
		}
		value, err := json.Marshal(definition.Seed(cfg))
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", definition.Kind, err)
		}
		value, err = definition.Decode(value)
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", definition.Kind, err)
		}
		resource, err = s.store.PutResource(ctx, store.ResourceWrite{Kind: definition.Kind, Value: value, Actor: "system:migration", Source: "legacy-config", RequestID: "import:" + definition.Kind, ExpectedVersion: 0, At: time.Now().UTC()})
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", definition.Kind, err)
		}
		versions[definition.Kind] = resource.Version
	}
	return versions, nil
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
