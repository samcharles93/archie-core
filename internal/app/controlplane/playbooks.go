package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

// PlaybooksKind is the control-plane resource holding the org's EDA playbook
// documents.
const PlaybooksKind = "eda-playbooks"

// PlaybookCollection is the eda-playbooks document: playbook sources under
// their stable ids.
type PlaybookCollection struct {
	Playbooks []PlaybookEntry `json:"playbooks"`
}

type PlaybookEntry struct {
	ID   string `json:"id"`
	YAML string `json:"yaml"`
}

func playbooksDefinition() Definition {
	return Definition{
		Kind:      PlaybooksKind,
		Title:     "EDA playbooks",
		Document:  PlaybookCollection{},
		ApplyMode: "live",
		Seed:      func(config.Config) any { return PlaybookCollection{Playbooks: []PlaybookEntry{}} },
		Validate: func(input []byte) error {
			return validateAs(input, func(c PlaybookCollection) error {
				_, err := CompilePlaybooks(c)
				return err
			})
		},
	}
}

// CompilePlaybooks compiles a collection against the module vocabulary. Any
// invalid playbook fails the set.
func CompilePlaybooks(c PlaybookCollection) (*playbook.Store, error) {
	docs := make([]playbook.Document, 0, len(c.Playbooks))
	seen := map[string]bool{}
	for _, entry := range c.Playbooks {
		if entry.ID == "" || seen[entry.ID] {
			return nil, fmt.Errorf("%w: playbook id %q is empty or repeated", ErrValidation, entry.ID)
		}
		seen[entry.ID] = true
		docs = append(docs, playbook.Document{ID: entry.ID, YAML: []byte(entry.YAML)})
	}
	store, err := playbook.Compile(docs, module.New())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return store, nil
}

// PlaybookProjector merges an installed package's playbooks into the org's
// eda-playbooks resource. Ids owned by an operator or another package are
// refused.
type PlaybookProjector struct {
	store  ResourceStore
	ledger storepkg.ProjectionLedger
}

var _ storepkg.FamilyProjector = (*PlaybookProjector)(nil)

func NewPlaybookPackageProjector(resources ResourceStore, ledger storepkg.ProjectionLedger) (*PlaybookProjector, error) {
	if resources == nil || ledger == nil {
		return nil, errors.New("control plane: playbook projection needs a resource store and a contributions ledger")
	}
	return &PlaybookProjector{store: resources, ledger: ledger}, nil
}

func (p *PlaybookProjector) Apply(ctx context.Context, orgID, name, digest string, contents map[string][]byte) error {
	incoming := make([]PlaybookEntry, 0, len(contents))
	recorded := make([]storepkg.ProjectionEntry, 0, len(contents))
	for contentName, content := range contents {
		id := path.Base(contentName)
		incoming = append(incoming, PlaybookEntry{ID: id, YAML: string(content)})
		recorded = append(recorded, storepkg.ProjectionEntry{Family: storepkg.FamilyPlaybooks, EntryID: id})
	}
	slices.SortFunc(incoming, func(a, b PlaybookEntry) int { return strings.Compare(a.ID, b.ID) })
	err := projectDocument(ctx, p.store, orgID, PlaybooksKind, "package:"+name+"@"+digest, "package-project:"+digest, func(current []byte) ([]byte, error) {
		collection, err := decodePlaybooks(current)
		if err != nil {
			return nil, err
		}
		for _, entry := range collection.Playbooks {
			if slices.ContainsFunc(incoming, func(in PlaybookEntry) bool { return in.ID == entry.ID }) {
				return nil, fmt.Errorf("package %q: %w: playbook %q", name, storepkg.ErrContributionCollision, entry.ID)
			}
		}
		collection.Playbooks = append(collection.Playbooks, incoming...)
		return encodePlaybooks(collection)
	})
	if err != nil {
		return err
	}
	if err := p.ledger.Record(ctx, orgID, name, recorded); err != nil {
		return fmt.Errorf("record contributions of %q: %w", name, err)
	}
	return nil
}

func (p *PlaybookProjector) Withdraw(ctx context.Context, orgID, name, digest string) error {
	entries, err := p.ledger.Entries(ctx, orgID, name)
	if err != nil {
		return fmt.Errorf("entries of package %q: %w", name, err)
	}
	var withdrawn []string
	for _, entry := range entries {
		if entry.Family == storepkg.FamilyPlaybooks {
			withdrawn = append(withdrawn, entry.EntryID)
		}
	}
	if len(withdrawn) == 0 {
		return nil
	}
	err = projectDocument(ctx, p.store, orgID, PlaybooksKind, "package-withdraw:"+name+"@"+digest, "package-withdraw:"+digest, func(current []byte) ([]byte, error) {
		collection, err := decodePlaybooks(current)
		if err != nil {
			return nil, err
		}
		collection.Playbooks = slices.DeleteFunc(collection.Playbooks, func(e PlaybookEntry) bool { return slices.Contains(withdrawn, e.ID) })
		return encodePlaybooks(collection)
	})
	if err != nil {
		return err
	}
	return p.ledger.Forget(ctx, orgID, name, storepkg.FamilyPlaybooks)
}

func decodePlaybooks(value []byte) (PlaybookCollection, error) {
	collection := PlaybookCollection{Playbooks: []PlaybookEntry{}}
	if value == nil {
		return collection, nil
	}
	if err := json.Unmarshal(value, &collection); err != nil {
		return PlaybookCollection{}, fmt.Errorf("decode %s: %w", PlaybooksKind, err)
	}
	return collection, nil
}

func encodePlaybooks(collection PlaybookCollection) ([]byte, error) {
	if _, err := CompilePlaybooks(collection); err != nil {
		return nil, err
	}
	return json.Marshal(collection)
}
