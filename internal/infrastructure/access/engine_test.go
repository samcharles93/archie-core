package access

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// TestInvalidInstancePolicy pins that an instance policy that no longer
// compiles closes the instance without locking the operator out: only an
// instance admin working on the policies passes, so the policy can be
// repaired from the dashboard.
func TestInvalidInstancePolicy(t *testing.T) {
	broken := access.Policy{ID: "broken", Level: access.LevelInstance, Text: `permit(principal, action == Archie::Action::"nope", resource);`}
	policies := append(access.ShippedOrgPolicies(org.DefaultOrgID), access.ShippedOrgPolicies("acme")...)
	engine := New(append(policies, broken))
	if len(engine.Problems()) != 1 {
		t.Fatalf("problems = %v, want the broken instance policy", engine.Problems())
	}

	owner := func(id identity.IdentityID, o org.OrgID) access.Principal {
		return access.Principal{IdentityID: id, Org: o, Memberships: []org.Membership{{IdentityID: id, OrgID: o, Role: org.RoleOwner}}}
	}
	admin := owner("admin", org.DefaultOrgID)
	tests := []struct {
		name      string
		principal access.Principal
		action    access.Action
		kind      access.ResourceKind
		allowed   bool
	}{
		{"instance admin edits policies", admin, access.ActionManagePolicies, access.KindPolicy, true},
		{"instance admin reads policies", admin, access.ActionRead, access.KindPolicy, true},
		{"instance admin reads anything else", admin, access.ActionRead, access.KindDashboard, false},
		{"another org's owner edits policies", owner("acme-owner", "acme"), access.ActionManagePolicies, access.KindPolicy, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.Authorize(tt.principal, tt.action, access.Resource{Kind: tt.kind}, access.Context{})
			if got.Allowed != tt.allowed {
				t.Fatalf("allowed = %v, want %v (%+v)", got.Allowed, tt.allowed, got)
			}
		})
	}
}
