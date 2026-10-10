package controlplane

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// IdentityGrantsKind is the resource naming the credential services each
// identity may use. Applied live on each launch.
const IdentityGrantsKind = "identity-grants"

// identityGrants is the document: the root identity's services, and per named
// identity a list that replaces the root's. An identity with no entry
// inherits the root's; an empty list grants none.
type identityGrants struct {
	Root       []string            `json:"root"`
	Identities map[string][]string `json:"identities,omitempty"`
}

func identityGrantsDefinition() Definition {
	return Definition{
		Kind:      IdentityGrantsKind,
		Title:     "Identity grants",
		ApplyMode: "live",
		Document:  identityGrants{},
		Seed:      seedIdentityGrants,
		Validate:  validateIdentityGrants,
	}
}

func seedIdentityGrants(cfg config.Config) any {
	doc := identityGrants{Root: cfg.GrantedCredentials, Identities: map[string][]string{}}
	for _, id := range cfg.Identities {
		if id.GrantedCredentials != nil {
			doc.Identities[id.Name] = *id.GrantedCredentials
		}
	}
	return doc
}

func validateIdentityGrants(input []byte) error {
	return validateAs(input, func(doc identityGrants) error {
		if err := validServices("root", doc.Root); err != nil {
			return err
		}
		for name, services := range doc.Identities {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("identity grants: an identity name is empty")
			}
			if err := validServices(name, services); err != nil {
				return err
			}
		}
		return nil
	})
}

func validServices(owner string, services []string) error {
	seen := make(map[string]bool, len(services))
	for _, service := range services {
		if strings.TrimSpace(service) == "" || seen[service] {
			return fmt.Errorf("identity grants for %s: services must be non-empty and listed once", owner)
		}
		seen[service] = true
	}
	return nil
}

// layerCredentials layers the credential bindings and the identity grants:
// each is its own resource, applies without a restart, and a stored value
// replaces the file's outright.
func layerCredentials(ctx context.Context, reader controlplanerpc.ResourceReader, versions map[string]int64, out *config.Config) error {
	if err := layerResource(ctx, reader, versions, CredentialBindingsKind, func(bindings []credentialBinding) error {
		out.Containers.Credentials = credentialBindingsSettings(bindings)
		return nil
	}); err != nil {
		return err
	}
	return layerResource(ctx, reader, versions, IdentityGrantsKind, func(doc identityGrants) error {
		applyIdentityGrants(out, doc)
		return nil
	})
}

// applyIdentityGrants replaces every identity's grants with the document's.
func applyIdentityGrants(out *config.Config, doc identityGrants) {
	out.GrantedCredentials = doc.Root
	for i := range out.Identities {
		if services, ok := doc.Identities[out.Identities[i].Name]; ok {
			out.Identities[i].GrantedCredentials = &services
		} else {
			out.Identities[i].GrantedCredentials = nil
		}
	}
}
