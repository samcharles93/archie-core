package controlplane

import (
	"encoding/json"
	"fmt"
	"path"

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

// NewPlaybookPackageProjector builds the projector for storepkg.FamilyPlaybooks.
func NewPlaybookPackageProjector(resources ResourceStore, ledger storepkg.ProjectionLedger) (storepkg.FamilyProjector, error) {
	return newListProjector(resources, ledger, listFamily[PlaybookEntry]{
		kind:   PlaybooksKind,
		family: storepkg.FamilyPlaybooks,
		decode: func(value []byte) ([]PlaybookEntry, error) {
			c, err := decodePlaybooks(value)
			return c.Playbooks, err
		},
		encode: func(entries []PlaybookEntry) ([]byte, error) {
			return encodePlaybooks(PlaybookCollection{Playbooks: entries})
		},
		id: func(e PlaybookEntry) string { return e.ID },
		parse: func(name string, content []byte) (PlaybookEntry, error) {
			return PlaybookEntry{ID: path.Base(name), YAML: string(content)}, nil
		},
	})
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
