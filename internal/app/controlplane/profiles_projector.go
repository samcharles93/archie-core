package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

// profileFile is one contributed profile: a name and the profile it defines.
// The image is a reference to a published image, never a build.
type profileFile struct {
	Name    string   `yaml:"name"`
	Image   string   `yaml:"image"`
	Kit     string   `yaml:"kit"`
	Adapter string   `yaml:"adapter"`
	Tools   []string `yaml:"tools"`
}

// ProfileProjector merges an installed package's agent profiles into the
// org's agent-profiles resource. Names owned by an operator or another
// package are refused.
type ProfileProjector struct {
	store  ResourceStore
	ledger storepkg.ProjectionLedger
}

var _ storepkg.FamilyProjector = (*ProfileProjector)(nil)

func NewProfilePackageProjector(resources ResourceStore, ledger storepkg.ProjectionLedger) (*ProfileProjector, error) {
	if resources == nil || ledger == nil {
		return nil, errors.New("control plane: profile projection needs a resource store and a contributions ledger")
	}
	return &ProfileProjector{store: resources, ledger: ledger}, nil
}

func (p *ProfileProjector) Apply(ctx context.Context, orgID, name, digest string, contents map[string][]byte) error {
	incoming := make(map[string]agentProfile, len(contents))
	recorded := make([]storepkg.ProjectionEntry, 0, len(contents))
	for contentName, content := range contents {
		var file profileFile
		if err := yaml.Unmarshal(content, &file); err != nil {
			return fmt.Errorf("contributed profile %q: %w", contentName, err)
		}
		if file.Name == "" {
			return fmt.Errorf("contributed profile %q has no name", contentName)
		}
		incoming[file.Name] = agentProfile{Image: file.Image, Kit: file.Kit, Adapter: file.Adapter, Tools: file.Tools}
		recorded = append(recorded, storepkg.ProjectionEntry{Family: storepkg.FamilyProfiles, EntryID: file.Name})
	}
	err := projectDocument(ctx, p.store, orgID, AgentProfileKind, "package:"+name+"@"+digest, "package-project:"+digest, func(current []byte) ([]byte, error) {
		profiles, err := decodeProfiles(current)
		if err != nil {
			return nil, err
		}
		for profile, value := range incoming {
			if _, taken := profiles[profile]; taken {
				return nil, fmt.Errorf("package %q: %w: profile %q", name, storepkg.ErrContributionCollision, profile)
			}
			profiles[profile] = value
		}
		return encodeProfiles(profiles)
	})
	if err != nil {
		return err
	}
	if err := p.ledger.Record(ctx, orgID, name, recorded); err != nil {
		return fmt.Errorf("record contributions of %q: %w", name, err)
	}
	return nil
}

func (p *ProfileProjector) Withdraw(ctx context.Context, orgID, name, digest string) error {
	entries, err := p.ledger.Entries(ctx, orgID, name)
	if err != nil {
		return fmt.Errorf("entries of package %q: %w", name, err)
	}
	var withdrawn []string
	for _, entry := range entries {
		if entry.Family == storepkg.FamilyProfiles {
			withdrawn = append(withdrawn, entry.EntryID)
		}
	}
	if len(withdrawn) == 0 {
		return nil
	}
	err = projectDocument(ctx, p.store, orgID, AgentProfileKind, "package-withdraw:"+name+"@"+digest, "package-withdraw:"+digest, func(current []byte) ([]byte, error) {
		profiles, err := decodeProfiles(current)
		if err != nil {
			return nil, err
		}
		for profile := range profiles {
			if slices.Contains(withdrawn, profile) {
				delete(profiles, profile)
			}
		}
		return encodeProfiles(profiles)
	})
	if err != nil {
		return err
	}
	return p.ledger.Forget(ctx, orgID, name, storepkg.FamilyProfiles)
}

func decodeProfiles(value []byte) (map[string]agentProfile, error) {
	profiles := map[string]agentProfile{}
	if value == nil {
		return profiles, nil
	}
	if err := json.Unmarshal(value, &profiles); err != nil {
		return nil, fmt.Errorf("decode %s: %w", AgentProfileKind, err)
	}
	if profiles == nil {
		profiles = map[string]agentProfile{}
	}
	return profiles, nil
}

func encodeProfiles(profiles map[string]agentProfile) ([]byte, error) {
	value, err := json.Marshal(profiles)
	if err != nil {
		return nil, err
	}
	if err := validateAgentProfiles(value); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return value, nil
}
