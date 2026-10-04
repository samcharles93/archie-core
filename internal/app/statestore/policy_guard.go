package statestore

import (
	"context"
	"slices"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	accessengine "github.com/samcharles93/archie-core/internal/infrastructure/access"
)

// ownerDirectory is what the guard needs from the store beyond policies.
type ownerDirectory interface {
	access.PrincipalSource
	ListOrgs(ctx context.Context) ([]org.Org, error)
	OrgOwners(ctx context.Context, orgID org.OrgID) ([]identity.IdentityID, error)
}

// guardedPolicies checks every policy change before it is stored: the text
// must compile against the schema, and the resulting chain must leave each
// org an owner who may still manage its policies.
type guardedPolicies struct {
	access.PolicyStore
	owners ownerDirectory
}

func (g guardedPolicies) PutPolicy(ctx context.Context, p access.Policy) (int64, error) {
	validator, err := accessengine.New(nil)
	if err != nil {
		return 0, err
	}
	if err := validator.Validate(p); err != nil {
		return 0, err
	}
	if err := g.checkLockout(ctx, func(current []access.Policy) []access.Policy {
		return append(slices.DeleteFunc(current, func(c access.Policy) bool { return samePolicy(c, p) }), p)
	}); err != nil {
		return 0, err
	}
	return g.PolicyStore.PutPolicy(ctx, p)
}

func (g guardedPolicies) DeletePolicy(ctx context.Context, p access.Policy) error {
	if err := g.checkLockout(ctx, func(current []access.Policy) []access.Policy {
		return slices.DeleteFunc(current, func(c access.Policy) bool { return samePolicy(c, p) })
	}); err != nil {
		return err
	}
	return g.PolicyStore.DeletePolicy(ctx, p)
}

func (g guardedPolicies) checkLockout(ctx context.Context, change func([]access.Policy) []access.Policy) error {
	current, err := g.ListPolicies(ctx)
	if err != nil {
		return err
	}
	engine, err := accessengine.New(change(current))
	if err != nil {
		return err
	}
	orgs, err := g.owners.ListOrgs(ctx)
	if err != nil {
		return err
	}
	for _, o := range orgs {
		ok, err := g.ownerCanManage(ctx, engine, o.ID)
		if err != nil {
			return err
		}
		if !ok {
			return access.ErrPolicyLockout
		}
	}
	return nil
}

func (g guardedPolicies) ownerCanManage(ctx context.Context, engine *accessengine.Engine, orgID org.OrgID) (bool, error) {
	owners, err := g.owners.OrgOwners(ctx, orgID)
	if err != nil {
		return false, err
	}
	principals := make([]access.Principal, 0, len(owners)+1)
	if orgID == org.DefaultOrgID {
		principals = append(principals, access.SharedTokenOwner())
	}
	for _, id := range owners {
		principal, err := g.owners.PrincipalFor(ctx, id)
		if err != nil {
			return false, err
		}
		principal.Org = orgID
		principals = append(principals, principal)
	}
	if len(principals) == 0 {
		// An org nobody owns cannot be locked out by a policy change.
		return true, nil
	}
	resource := access.Resource{Kind: access.KindPolicy, Org: orgID}
	for _, principal := range principals {
		if engine.Authorize(principal, access.ActionManagePolicies, resource, access.Context{}).Allowed {
			return true, nil
		}
	}
	return false, nil
}

func samePolicy(a, b access.Policy) bool {
	return a.ID == b.ID && a.Level == b.Level && a.OrgID == b.OrgID &&
		a.WorkspaceID == b.WorkspaceID && a.ObjectKind == b.ObjectKind && a.ObjectID == b.ObjectID
}
