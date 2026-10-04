package storepkg

import (
	"context"
	"fmt"
)

// The descriptor families a package's Contributes names, and the family a
// projector takes over.
const (
	FamilyWorkflows = "workflows"
	FamilyPrompts   = "prompts"
	FamilySkills    = "skills"
	FamilyMCPServer = "mcpServers"
	FamilyDefaults  = "defaults"
	FamilyPlaybooks = "playbooks"
	FamilyProfiles  = "profiles"
)

// families is the family order projection follows, for a deterministic
// install and withdrawal.
var families = []string{FamilyWorkflows, FamilyPrompts, FamilySkills, FamilyMCPServer, FamilyDefaults, FamilyPlaybooks, FamilyProfiles}

// familyNames returns the names one descriptor family contributes.
func (c Contributions) familyNames(family string) []string {
	switch family {
	case FamilyWorkflows:
		return c.Workflows
	case FamilyPrompts:
		return c.Prompts
	case FamilySkills:
		return c.Skills
	case FamilyMCPServer:
		return c.MCPServers
	case FamilyDefaults:
		return c.Defaults
	case FamilyPlaybooks:
		return c.Playbooks
	case FamilyProfiles:
		return c.Profiles
	default:
		return nil
	}
}

// FamilyProjector applies one descriptor family of an installed package into
// the org resource that family contributes to, and withdraws its
// contributions again on removal. Installing therefore makes a contribution
// usable and removing stops it, both without restarting processes.
type FamilyProjector interface {
	// Apply carries the contribution into the org's resources. contents maps
	// each contributed name to its layer content.
	Apply(ctx context.Context, orgID, name, digest string, contents map[string][]byte) error
	// Withdraw takes the contribution back out for an org's package.
	Withdraw(ctx context.Context, orgID, name, digest string) error
}

// ProjectionEntry records one family's entry a package contributed to its org
// resource, so removal can take exactly those entries back out.
type ProjectionEntry struct {
	Family  string
	EntryID string
}

// ProjectionLedger keeps the entries an org's installed package projected.
// Implementations live with the installed packages they describe.
type ProjectionLedger interface {
	Record(context.Context, string, string, []ProjectionEntry) error
	Entries(ctx context.Context, orgID, name string) ([]ProjectionEntry, error)
	Forget(ctx context.Context, orgID, name, family string) error
}

// resolveFamilyContents resolves every projected family of a package before
// anything persists: a contribution the layer cannot back is an install that
// can never become usable.
func (s Service) resolveFamilyContents(descriptor Descriptor, layer []byte) (map[string]map[string][]byte, error) {
	contents := map[string]map[string][]byte{}
	for _, family := range families {
		if _, ok := s.Projections[family]; !ok {
			continue
		}
		entries, err := s.familyContents(descriptor, layer, family)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			continue
		}
		contents[family] = entries
	}
	return contents, nil
}

// familyContents resolves one family's contributed names against the fetched
// layer. A contributed name without layer content is an install that can
// never make its contribution usable, so it refuses here.
func (s Service) familyContents(descriptor Descriptor, layer []byte, family string) (map[string][]byte, error) {
	names := descriptor.Contributes.familyNames(family)
	if len(names) == 0 {
		return nil, nil
	}
	files, err := FilesFromLayer(layer)
	if err != nil {
		return nil, err
	}
	contents := make(map[string][]byte, len(names))
	for _, name := range names {
		content, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("contributed %s %q is not a package file", family, name)
		}
		declared := false
		for _, file := range descriptor.Files {
			if file.Path == name {
				declared = true
				break
			}
		}
		if !declared {
			return nil, fmt.Errorf("contributed %s %q is not a declared package file", family, name)
		}
		contents[name] = content
	}
	return contents, nil
}

// project carries a new installation's contributions into the org resources
// their families project to. A family with no projector is persisted but not
// projected.
func (s Service) project(ctx context.Context, installed Installed, contents map[string]map[string][]byte) error {
	for _, family := range families {
		entries, ok := contents[family]
		if !ok || len(entries) == 0 {
			continue
		}
		projector, ok := s.Projections[family]
		if !ok {
			continue
		}
		if err := projector.Apply(ctx, installed.OrgID, installed.Name, installed.Digest, entries); err != nil {
			return fmt.Errorf("project %s of package %q: %w", family, installed.Name, err)
		}
	}
	return nil
}

// withdraw takes an installation's contributions back out of the org
// resources they were projected into, before the package record goes: a
// removal that fails its withdrawal keeps the package installed and its
// contributions usable. Families without projectors withdraw nothing.
func (s Service) withdraw(ctx context.Context, installed Installed) error {
	for _, family := range families {
		projector, ok := s.Projections[family]
		if !ok {
			continue
		}
		if err := projector.Withdraw(ctx, installed.OrgID, installed.Name, installed.Digest); err != nil {
			return fmt.Errorf("withdraw %s of package %q: %w", family, installed.Name, err)
		}
	}
	return nil
}
