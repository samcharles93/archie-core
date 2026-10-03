package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// projectionActor is the audit identity every package projection carries: the
// write is made by the installed package, never by a request body.
const projectionActor = "archie-state-store"

// projectWriteAttempts bounds one apply or withdrawal: each attempt re-reads
// the org's document and re-merges, so a document the operator edited during
// the projection is folded in, and only a persistently moving document
// refuses the projection.
const projectWriteAttempts = 3

// WorkflowProjector carries an installed package's workflow contributions
// into the org's workflow-definitions resource, the same document an operator
// edits on the dashboard. The readers the
// daemon holds read that resource per dispatch, so a projected workflow is
// usable at the next dispatch and a withdrawn one stops dispatching, without
// restarting processes.
//
// Apply merges: a contributed id that belongs to an operator or to another
// package is refused (storepkg.ErrContributionCollision), never silently
// replaced.
type WorkflowProjector struct {
	store  ResourceStore
	ledger storepkg.ProjectionLedger
	steps  workflow.StepRegistry
}

var _ storepkg.FamilyProjector = (*WorkflowProjector)(nil)

// NewWorkflowPackageProjector builds the projector for storepkg.FamilyWorkflows
// on the workflow step vocabulary the composition root registered: the
// vocabulary the projected definitions are compiled and validated against,
// the same one the control plane validates dashboard edits with.
func NewWorkflowPackageProjector(resources ResourceStore, ledger storepkg.ProjectionLedger, manager *workflow.Manager) (*WorkflowProjector, error) {
	steps, err := stepRegistry(manager)
	if err != nil {
		return nil, err
	}
	if resources == nil || ledger == nil {
		return nil, errors.New("control plane: workflow projection needs a resource store and a contributions ledger")
	}
	return &WorkflowProjector{store: resources, ledger: ledger, steps: steps}, nil
}

func (p *WorkflowProjector) Apply(ctx context.Context, orgID, name, digest string, contents map[string][]byte) error {
	entries := make([]workflow.WorkflowDefinitionEntry, 0, len(contents))
	recorded := make([]storepkg.ProjectionEntry, 0, len(contents))
	for contentName, content := range contents {
		id, err := workflow.DefinitionID(string(content))
		if err != nil {
			return fmt.Errorf("contributed workflow %q: %w", contentName, err)
		}
		entries = append(entries, workflow.WorkflowDefinitionEntry{ID: id, YAML: string(content)})
		recorded = append(recorded, storepkg.ProjectionEntry{Family: storepkg.FamilyWorkflows, EntryID: id})
	}
	// One apply, one request: a retried PutResource reuses the same request ID
	// and answers once, while a later install of the same package writes a
	// revision of its own.
	source := "package:" + name + "@" + digest
	if err := p.write(ctx, orgID, source, "package-project:"+digest, func(collection workflow.WorkflowDefinitionCollection) (workflow.WorkflowDefinitionCollection, error) {
		return mergeContribution(collection, entries, name)
	}); err != nil {
		return err
	}
	if err := p.ledger.Record(ctx, orgID, name, recorded); err != nil {
		return fmt.Errorf("record contributions of %q: %w", name, err)
	}
	return nil
}

func (p *WorkflowProjector) Withdraw(ctx context.Context, orgID, name, digest string) error {
	entries, err := p.ledger.Entries(ctx, orgID, name)
	if err != nil {
		return fmt.Errorf("entries of package %q: %w", name, err)
	}
	withdrawn := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Family == storepkg.FamilyWorkflows {
			withdrawn[entry.EntryID] = struct{}{}
		}
	}
	if len(withdrawn) == 0 {
		return nil
	}
	if err := p.write(ctx, orgID, "package-withdraw:"+name+"@"+digest, "package-withdraw:"+digest,
		func(collection workflow.WorkflowDefinitionCollection) (workflow.WorkflowDefinitionCollection, error) {
			merged := make([]workflow.WorkflowDefinitionEntry, 0, len(collection.Definitions))
			for _, item := range collection.Definitions {
				if _, gone := withdrawn[item.ID]; gone {
					continue
				}
				merged = append(merged, item)
			}
			return workflow.WorkflowDefinitionCollection{Definitions: merged}, nil
		}); err != nil {
		return err
	}
	return p.ledger.Forget(ctx, orgID, name, storepkg.FamilyWorkflows)
}

// write merges an org's stored workflow-definitions document and puts it
// back, bounded by projectWriteAttempts: each attempt re-reads and re-merges,
// so a document the operator edited during the projection is folded in, and
// the write carries the ledger's replay identity of one episode.
func (p *WorkflowProjector) write(ctx context.Context, orgID, source, requestPrefix string, merge func(workflow.WorkflowDefinitionCollection) (workflow.WorkflowDefinitionCollection, error)) error {
	requestID := requestPrefix + ":" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	for attempt := 0; ; attempt++ {
		collection, version := p.collection(ctx, orgID)
		merged, err := merge(collection)
		if err != nil {
			return err
		}
		value, err := encodeWorkflowDefinitions(merged, p.steps)
		if err != nil {
			return err
		}
		if _, err := p.store.PutResource(ctx, storecontract.ResourceWrite{
			OrgID: orgID, Kind: WorkflowDefinitionsKind, Value: value, ExpectedVersion: version,
			Actor: projectionActor, Source: source, RequestID: requestID,
		}); errors.Is(err, storecontract.ErrResourceVersionConflict) && attempt < projectWriteAttempts-1 {
			continue
		} else if err != nil {
			return err
		}
		return nil
	}
}

// collection reads an org's workflow-definitions document. An org that never
// received one starts from the shipped definitions, the state a first write
// seeds, so projection leaves the org with one answer either way.
func (p *WorkflowProjector) collection(ctx context.Context, orgID string) (workflow.WorkflowDefinitionCollection, int64) {
	resource, err := p.store.Resource(ctx, orgID, WorkflowDefinitionsKind)
	if errors.Is(err, storecontract.ErrResourceNotFound) {
		return workflow.ShippedDefinitions(), 0
	}
	if err != nil {
		return workflow.WorkflowDefinitionCollection{}, 0
	}
	collection, err := workflow.DecodeDefinitionCollection(resource.Value, p.steps)
	if err != nil {
		return workflow.WorkflowDefinitionCollection{}, 0
	}
	return collection, resource.Version
}

// mergeContribution merges a package's contributed entries into an org's
// collection. An operator's entry with the same workflow id is a refusal, not
// a silent replacement; the package's own entries reach here only after its
// ledger rows were cleared, so colliding is always foreign work.
func mergeContribution(base workflow.WorkflowDefinitionCollection, contributed []workflow.WorkflowDefinitionEntry, name string) (workflow.WorkflowDefinitionCollection, error) {
	replacing := make(map[string]struct{}, len(contributed))
	for _, entry := range contributed {
		replacing[entry.ID] = struct{}{}
	}
	merged := make([]workflow.WorkflowDefinitionEntry, 0, len(base.Definitions)+len(contributed))
	for _, entry := range base.Definitions {
		if _, incoming := replacing[entry.ID]; incoming {
			return workflow.WorkflowDefinitionCollection{}, fmt.Errorf("package %q: %w: workflow %q", name, storepkg.ErrContributionCollision, entry.ID)
		}
		merged = append(merged, entry)
	}
	merged = append(merged, contributed...)
	return workflow.WorkflowDefinitionCollection{Definitions: merged}, nil
}
