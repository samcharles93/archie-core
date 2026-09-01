// Package memory implements domain/memory.MemoryEngine against concrete
// backends. See docs/architecture/migration-decisions.md#5-memory-placement-and-storage
// for the record/identity model this package's builtin engine follows.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/memory/builtin"
)

// EngineName is the name BuiltinEngine registers under and the default the
// config surface (archie-core-1786637490043) resolves to when no engine is
// configured.
const EngineName = "builtin"

// markerPrefix opens the leading line every block written through this
// engine carries, embedding the record's stable ID. See the migration
// decision this package implements: an ID assigned once at Write, never
// derived from content, so a later edit elsewhere in the file can never
// orphan a Forget.
const (
	markerPrefix = "<!--mem:"
	markerSuffix = "-->"
)

// defaultSectionName is the section an Observation with an empty Kind is
// written to.
const defaultSectionName = "general"

// BuiltinEngine implements domain/memory.MemoryEngine over one
// internal/memory/builtin.Store per identity. internal/memory/builtin is
// unchanged by this package -- see the migration decision this
// implements for why.
type BuiltinEngine struct {
	root         string
	maxFileBytes int

	mu     sync.Mutex
	stores map[string]*builtin.Store // keyed by identity, lazily created
}

// NewBuiltinEngine builds an engine that persists each identity's
// observations under its own subdirectory of root, named
// hex(sha256(identity)) -- never the identity string itself, so an
// arbitrary identity can never traverse outside root and two identities
// differing only in filesystem-equivalent characters can never collide.
// maxFileBytes <= 0 uses the store's own default.
func NewBuiltinEngine(root string, maxFileBytes int) *BuiltinEngine {
	return &BuiltinEngine{root: root, maxFileBytes: maxFileBytes, stores: make(map[string]*builtin.Store)}
}

func (e *BuiltinEngine) Name() string    { return EngineName }
func (e *BuiltinEngine) Version() string { return "1" }

func (e *BuiltinEngine) Manifest() domainmemory.Manifest {
	return domainmemory.Manifest{RequiresNetwork: false}
}

func (e *BuiltinEngine) Bind(domainmemory.Registrar) {}

func (e *BuiltinEngine) Start(context.Context) error { return nil }

func (e *BuiltinEngine) Health(context.Context) domainmemory.Health {
	return domainmemory.Health{Status: domainmemory.HealthHealthy}
}

func (e *BuiltinEngine) Stop(context.Context) error { return nil }

// storeFor returns the identity's store, creating and caching it on first
// use.
func (e *BuiltinEngine) storeFor(identity string) (*builtin.Store, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.stores[identity]; ok {
		return s, nil
	}
	dir := filepath.Join(e.root, identityDir(identity))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("memory: builtin engine: identity directory %s unusable: %w", dir, err)
	}
	s, err := builtin.NewStore(filepath.Join(dir, "OBSERVATIONS.md"), e.maxFileBytes)
	if err != nil {
		return nil, err
	}
	e.stores[identity] = s
	return s, nil
}

// identityDir maps an identity to a filesystem-safe, collision-resistant
// directory name.
func identityDir(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

// sectionFor returns the section an Observation's Kind maps to, and
// validates it the same way builtin already validates any section name --
// so an invalid Kind fails with the same error shape memory_edit already
// returns for an invalid section, not a new one.
func sectionFor(kind string) (string, error) {
	if kind == "" {
		kind = defaultSectionName
	}
	if err := builtin.ValidateSectionName(kind); err != nil {
		return "", err
	}
	return kind, nil
}

func (e *BuiltinEngine) Write(_ context.Context, obs domainmemory.Observation) (domainmemory.Record, error) {
	if err := obs.Validate(); err != nil {
		return domainmemory.Record{}, err
	}
	section, err := sectionFor(obs.Kind)
	if err != nil {
		return domainmemory.Record{}, err
	}
	s, err := e.storeFor(obs.Identity)
	if err != nil {
		return domainmemory.Record{}, err
	}

	id := uuid.NewString()
	marked := markerPrefix + id + markerSuffix + "\n" + obs.Content
	if _, err := s.Add(section, marked); err != nil {
		return domainmemory.Record{}, err
	}

	return domainmemory.Record{
		ID:       id,
		Identity: obs.Identity,
		Kind:     section,
		Content:  obs.Content,
		At:       obs.At,
		Metadata: obs.Metadata,
	}, nil
}

func (e *BuiltinEngine) Query(ctx context.Context, q domainmemory.Query) ([]domainmemory.Record, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	all, err := e.List(ctx, q.Identity)
	if err != nil {
		return nil, err
	}
	if q.Text == "" {
		return limitRecords(all, q.Limit), nil
	}
	var matched []domainmemory.Record
	for _, r := range all {
		if strings.Contains(r.Content, q.Text) {
			matched = append(matched, r)
		}
	}
	return limitRecords(matched, q.Limit), nil
}

func limitRecords(records []domainmemory.Record, limit int) []domainmemory.Record {
	if limit > 0 && len(records) > limit {
		return records[:limit]
	}
	return records
}

func (e *BuiltinEngine) List(_ context.Context, identity string) ([]domainmemory.Record, error) {
	if identity == "" {
		return nil, errors.New("memory: builtin engine: identity must not be empty")
	}
	s, err := e.storeFor(identity)
	if err != nil {
		return nil, err
	}
	return parseRecords(identity, s.Render()), nil
}

func (e *BuiltinEngine) Forget(_ context.Context, id string) error {
	if id == "" {
		return errors.New("memory: builtin engine: id must not be empty")
	}
	marker := markerPrefix + id + markerSuffix
	e.mu.Lock()
	stores := make([]*builtin.Store, 0, len(e.stores))
	for _, s := range e.stores {
		stores = append(stores, s)
	}
	e.mu.Unlock()

	// Forgetting an id that does not exist is not an error (contract.go's
	// Forget doc): the end state -- id absent -- already holds whether or
	// not any store ever had it, so checking every known identity's store
	// and returning nil regardless of a miss is correct, not a fallback
	// masking a real failure.
	for _, s := range stores {
		section, ok := sectionOf(s.Render(), id)
		if !ok {
			continue
		}
		if _, err := s.Remove(section, marker, true); err != nil {
			return err
		}
		return nil
	}
	return nil
}

// sectionOf finds which section of rendered carries a block whose marker
// names id, so Forget can call Store.Remove with the section it actually
// requires without a mutating probe. ok is false if no block does.
func sectionOf(rendered, id string) (section string, ok bool) {
	for _, rec := range parseRecords("", rendered) {
		if rec.ID == id {
			return rec.Kind, true
		}
	}
	return "", false
}
