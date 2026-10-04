package statestore

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

type memPolicies struct {
	access.PolicyStore
	policies []access.Policy
}

func (m *memPolicies) ListPolicies(context.Context) ([]access.Policy, error) {
	return slices.Clone(m.policies), nil
}

func (m *memPolicies) PutPolicy(_ context.Context, p access.Policy) (int64, error) {
	m.policies = append(slices.DeleteFunc(m.policies, func(c access.Policy) bool { return samePolicy(c, p) }), p)
	return 1, nil
}

func (m *memPolicies) DeletePolicy(_ context.Context, p access.Policy) error {
	m.policies = slices.DeleteFunc(m.policies, func(c access.Policy) bool { return samePolicy(c, p) })
	return nil
}

type oneOwnerOrg struct{}

const testOrg org.OrgID = "default"

func (oneOwnerOrg) ListOrgs(context.Context) ([]org.Org, error) { return []org.Org{{ID: testOrg}}, nil }

func (oneOwnerOrg) OrgOwners(context.Context, org.OrgID) ([]identity.IdentityID, error) {
	return []identity.IdentityID{"sam"}, nil
}

func (oneOwnerOrg) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	return access.Principal{IdentityID: id, Org: testOrg, Memberships: []org.Membership{{IdentityID: id, OrgID: testOrg, Role: org.RoleOwner}}}, nil
}

func TestPolicyGuard(t *testing.T) {
	shipped := access.ShippedOrgPolicies(testOrg)
	orgPolicy := func(id, text string) access.Policy {
		return access.Policy{ID: id, Level: access.LevelOrg, OrgID: testOrg, Text: text}
	}
	tests := []struct {
		name   string
		put    *access.Policy
		delete *access.Policy
		want   error
	}{
		{name: "harmless addition", put: new(orgPolicy("viewers-read", `permit(principal, action == Archie::Action::"read", resource);`))},
		{name: "text that does not compile", put: new(orgPolicy("broken", `permit(principal, action == Archie::Action::"nope", resource);`)), want: access.ErrPolicyInvalidText},
		{name: "forbid locks owners out", put: new(orgPolicy("lock", `forbid(principal, action == Archie::Action::"manage_policies", resource);`)), want: access.ErrPolicyLockout},
		{name: "deleting the owner policy while the admin policy still covers owners", delete: &shipped[3]},
		{name: "deleting the read policy", delete: &shipped[0]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memPolicies{policies: slices.Clone(shipped)}
			guard := guardedPolicies{PolicyStore: store, owners: oneOwnerOrg{}}
			var err error
			if tt.put != nil {
				_, err = guard.PutPolicy(t.Context(), *tt.put)
			} else {
				err = guard.DeletePolicy(t.Context(), *tt.delete)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
