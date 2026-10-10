package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// OrgModelPolicyKind is the instance's per-org model policy. Only the
// system org writes it.
const OrgModelPolicyKind = "org-model-policy"

// orgModelPolicy is what one org may use. An org with no entry may use
// nothing until the instance admin allows it; the system org is never
// restricted.
type orgModelPolicy struct {
	// Allowed lists the "provider/model" refs the org may use; "provider/*"
	// allows every model of an instance provider.
	Allowed []string `json:"allowed"`
	// OwnProviders lets the org add providers under its own credentials.
	OwnProviders bool `json:"own_providers"`
}

func (p *orgModelPolicy) allows(ref string) bool {
	if p == nil {
		return false
	}
	provider, _, _ := strings.Cut(ref, "/")
	return slices.Contains(p.Allowed, ref) || slices.Contains(p.Allowed, provider+"/*")
}

func orgModelPolicyDefinition() Definition {
	return Definition{
		Kind: OrgModelPolicyKind, Title: "Org model policy", ApplyMode: "live",
		Document: map[string]orgModelPolicy{},
		Seed:     func(config.Config) any { return map[string]orgModelPolicy{} },
		Validate: func(input []byte) error {
			return validateAs(input, func(policies map[string]orgModelPolicy) error {
				for orgID, policy := range policies {
					if orgID == storecontract.DefaultOrgID {
						return fmt.Errorf("the system org uses every instance model; it takes no policy")
					}
					for _, ref := range policy.Allowed {
						if provider, model, ok := strings.Cut(ref, "/"); !ok || provider == "" || model == "" {
							return fmt.Errorf("org %q allows %q; use provider/model or provider/*", orgID, ref)
						}
					}
				}
				return nil
			})
		},
	}
}

// ModelsFor resolves orgID's effective aliases and its own providers: the
// instance aliases it may use, with its own aliases over them. An alias
// whose model the org may not use is left out, so it fails as unknown.
func (s *Server) ModelsFor(ctx context.Context, orgID string) (config.OrgModels, error) {
	instance, err := s.modelAliases(ctx, storecontract.DefaultOrgID)
	if err != nil || orgID == "" || orgID == storecontract.DefaultOrgID {
		return config.OrgModels{Org: orgID, Aliases: instance}, err
	}
	policy, err := s.orgPolicy(ctx, orgID)
	if err != nil {
		return config.OrgModels{}, err
	}
	providers, err := s.orgProviders(ctx, orgID, policy)
	if err != nil {
		return config.OrgModels{}, err
	}
	own, err := s.modelAliases(ctx, orgID)
	if err != nil {
		return config.OrgModels{}, err
	}
	aliases := make(map[string]string, len(instance)+len(own))
	for alias, ref := range instance {
		if policy.allows(ref) {
			aliases[alias] = ref
		}
	}
	for alias, ref := range own {
		if usable(ref, policy, providers) {
			aliases[alias] = ref
		} else {
			delete(aliases, alias)
		}
	}
	return config.OrgModels{Org: orgID, Aliases: aliases, Providers: providers}, nil
}

func usable(ref string, policy *orgModelPolicy, own map[string]config.Provider) bool {
	provider, _, _ := strings.Cut(ref, "/")
	if _, ok := own[provider]; ok {
		return true
	}
	return policy.allows(ref)
}

func (s *Server) orgPolicy(ctx context.Context, orgID string) (*orgModelPolicy, error) {
	var policies map[string]orgModelPolicy
	if err := s.readResource(ctx, storecontract.DefaultOrgID, OrgModelPolicyKind, &policies); err != nil {
		return nil, err
	}
	policy, ok := policies[orgID]
	if !ok {
		return nil, nil
	}
	return &policy, nil
}

// orgProviders is orgID's own providers, empty unless its policy allows them.
func (s *Server) orgProviders(ctx context.Context, orgID string, policy *orgModelPolicy) (map[string]config.Provider, error) {
	out := map[string]config.Provider{}
	if policy == nil || !policy.OwnProviders {
		return out, nil
	}
	var docs map[string]providerDocument
	if err := s.readResource(ctx, orgID, ProviderSettingsKind, &docs); err != nil {
		return nil, err
	}
	for name, doc := range docs {
		out[name] = config.Provider{Class: doc.Class, BaseURL: doc.BaseURL, APIKeyEnv: doc.APIKeyEnv}
	}
	return out, nil
}

func (s *Server) modelAliases(ctx context.Context, orgID string) (map[string]string, error) {
	aliases := map[string]string{}
	return aliases, s.readResource(ctx, orgID, ModelAliasesKind, &aliases)
}

// readResource decodes orgID's kind into into, leaving it untouched when the
// org has none.
func (s *Server) readResource(ctx context.Context, orgID, kind string, into any) error {
	resource, err := s.store.Resource(ctx, orgID, kind)
	if errors.Is(err, storecontract.ErrResourceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resource.Value, into); err != nil {
		return fmt.Errorf("read %s %s: %w", orgID, kind, err)
	}
	return nil
}

// checkOrgWrite applies the rules a resource's static validation cannot: the
// policy is the system org's alone, an org's providers need its policy's
// leave and its own credentials, and every alias must name a model the
// writing org may use.
func (s *Server) checkOrgWrite(ctx context.Context, orgID, kind string, value []byte) error {
	switch kind {
	case OrgModelPolicyKind:
		if orgID != storecontract.DefaultOrgID {
			return fmt.Errorf("%w: only the system org sets org model policy", ErrValidation)
		}
	case ProviderSettingsKind:
		if orgID != storecontract.DefaultOrgID {
			return s.checkOrgProviders(ctx, orgID, value)
		}
	case ModelAliasesKind:
		return s.checkAliasWrite(ctx, orgID, value)
	case WorkflowDefinitionsKind:
		return s.checkModelAliases(ctx, orgID, value)
	}
	return nil
}

func (s *Server) checkOrgProviders(ctx context.Context, orgID string, value []byte) error {
	var docs map[string]providerDocument
	if err := json.Unmarshal(value, &docs); err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	policy, err := s.orgPolicy(ctx, orgID)
	if err != nil {
		return err
	}
	if policy == nil || !policy.OwnProviders {
		return fmt.Errorf("%w: org %s may not add its own providers (instance Settings > Models > Org access)", ErrValidation, orgID)
	}
	var instance map[string]providerDocument
	if err := s.readResource(ctx, storecontract.DefaultOrgID, ProviderSettingsKind, &instance); err != nil {
		return err
	}
	for name, doc := range docs {
		if _, taken := instance[name]; taken {
			return fmt.Errorf("%w: provider %q is an instance provider; name yours differently", ErrValidation, name)
		}
		// The key comes from the org's own credential binding named after
		// the provider, never a secret this org could point at elsewhere.
		if doc.APIKey != (config.SecretRef{}) {
			return fmt.Errorf("%w: provider %q: an org provider takes its key from the org's credential binding named %q, not a secret reference", ErrValidation, name, name)
		}
	}
	return nil
}

func (s *Server) checkAliasWrite(ctx context.Context, orgID string, value []byte) error {
	var aliases map[string]string
	if err := json.Unmarshal(value, &aliases); err != nil {
		return err
	}
	if orgID == storecontract.DefaultOrgID {
		if _, ok := aliases[config.DefaultModelAlias]; !ok && len(aliases) > 0 {
			return fmt.Errorf("%w: model alias %q is required", ErrValidation, config.DefaultModelAlias)
		}
		return nil
	}
	policy, err := s.orgPolicy(ctx, orgID)
	if err != nil {
		return err
	}
	providers, err := s.orgProviders(ctx, orgID, policy)
	if err != nil {
		return err
	}
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		if !usable(aliases[alias], policy, providers) {
			return fmt.Errorf("%w: model alias %q: org %s may not use %q", ErrValidation, alias, orgID, aliases[alias])
		}
	}
	return nil
}
