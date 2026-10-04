package controlplane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

// projectDocument edits one org resource on behalf of an installed package.
// Each attempt re-reads the stored document and re-applies edit, so an
// operator edit made during the projection is folded in, and only a
// persistently moving document refuses it. edit receives nil for a resource
// the org never wrote.
func projectDocument(ctx context.Context, store ResourceStore, orgID, kind, source, requestPrefix string, edit func(current []byte) ([]byte, error)) error {
	requestID := requestPrefix + ":" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	for attempt := 0; ; attempt++ {
		var current []byte
		var version int64
		resource, err := store.Resource(ctx, orgID, kind)
		switch {
		case errors.Is(err, storecontract.ErrResourceNotFound):
		case err != nil:
			return err
		default:
			current, version = resource.Value, resource.Version
		}
		value, err := edit(current)
		if err != nil {
			return err
		}
		_, err = store.PutResource(ctx, storecontract.ResourceWrite{
			OrgID: orgID, Kind: kind, Value: value, ExpectedVersion: version,
			Actor: projectionActor, Source: source, RequestID: requestID,
		})
		if errors.Is(err, storecontract.ErrResourceVersionConflict) && attempt < projectWriteAttempts-1 {
			continue
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", kind, err)
		}
		return nil
	}
}

// listFamily describes a family whose resource is a list of entries with
// stable ids, merged into by installed packages.
type listFamily[E any] struct {
	kind, family string
	decode       func([]byte) ([]E, error)
	// encode validates the merged list and returns the document.
	encode func([]E) ([]byte, error)
	id     func(E) string
	// parse reads one contributed file into an entry.
	parse func(name string, content []byte) (E, error)
}

// listProjector merges an installed package's entries into the org's list
// resource. Ids owned by an operator or another package are refused.
type listProjector[E any] struct {
	store  ResourceStore
	ledger storepkg.ProjectionLedger
	listFamily[E]
}

func newListProjector[E any](resources ResourceStore, ledger storepkg.ProjectionLedger, family listFamily[E]) (*listProjector[E], error) {
	if resources == nil || ledger == nil {
		return nil, fmt.Errorf("control plane: %s projection needs a resource store and a contributions ledger", family.family)
	}
	return &listProjector[E]{store: resources, ledger: ledger, listFamily: family}, nil
}

func (p *listProjector[E]) Apply(ctx context.Context, orgID, name, digest string, contents map[string][]byte) error {
	incoming := make([]E, 0, len(contents))
	recorded := make([]storepkg.ProjectionEntry, 0, len(contents))
	for contentName, content := range contents {
		entry, err := p.parse(contentName, content)
		if err != nil {
			return fmt.Errorf("contributed %s %q: %w", p.family, contentName, err)
		}
		incoming = append(incoming, entry)
		recorded = append(recorded, storepkg.ProjectionEntry{Family: p.family, EntryID: p.id(entry)})
	}
	err := projectDocument(ctx, p.store, orgID, p.kind, "package:"+name+"@"+digest, "package-project:"+digest, func(current []byte) ([]byte, error) {
		entries, err := p.decode(current)
		if err != nil {
			return nil, err
		}
		for _, have := range entries {
			if slices.ContainsFunc(incoming, func(in E) bool { return p.id(in) == p.id(have) }) {
				return nil, fmt.Errorf("package %q: %w: %s %q", name, storepkg.ErrContributionCollision, p.family, p.id(have))
			}
		}
		return p.encode(append(entries, incoming...))
	})
	if err != nil {
		return err
	}
	if err := p.ledger.Record(ctx, orgID, name, recorded); err != nil {
		return fmt.Errorf("record contributions of %q: %w", name, err)
	}
	return nil
}

func (p *listProjector[E]) Withdraw(ctx context.Context, orgID, name, digest string) error {
	recorded, err := p.ledger.Entries(ctx, orgID, name)
	if err != nil {
		return fmt.Errorf("entries of package %q: %w", name, err)
	}
	var withdrawn []string
	for _, entry := range recorded {
		if entry.Family == p.family {
			withdrawn = append(withdrawn, entry.EntryID)
		}
	}
	if len(withdrawn) == 0 {
		return nil
	}
	err = projectDocument(ctx, p.store, orgID, p.kind, "package-withdraw:"+name+"@"+digest, "package-withdraw:"+digest, func(current []byte) ([]byte, error) {
		entries, err := p.decode(current)
		if err != nil {
			return nil, err
		}
		return p.encode(slices.DeleteFunc(entries, func(e E) bool { return slices.Contains(withdrawn, p.id(e)) }))
	})
	if err != nil {
		return err
	}
	return p.ledger.Forget(ctx, orgID, name, p.family)
}
