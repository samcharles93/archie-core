package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// memberOf builds a principal serving orgID with one org-wide membership.
func memberOf(id identity.IdentityID, orgID org.OrgID, role org.Role) access.Principal {
	return access.Principal{
		IdentityID:  id,
		Org:         orgID,
		Memberships: []org.Membership{{IdentityID: id, OrgID: orgID, Role: role}},
	}
}

// TestInstanceOwner pins who may see and edit instance policies. Since kgxw.4
// the shared token acts as the org-sys operator person, so ownership of org-sys
// is the test, not equality with the system identity; an owner of any other org
// and a lesser member of org-sys are refused.
func TestInstanceOwner(t *testing.T) {
	tests := []struct {
		name      string
		principal access.Principal
		has       bool
		want      bool
	}{
		{"the operator person owning org-sys is the instance owner", operator(), true, true},
		{"the system fallback owning org-sys is the instance owner", access.SharedTokenOwner(), true, true},
		{"no principal falls back to the system owner", access.Principal{}, false, true},
		{"an admin of org-sys is not the instance owner", memberOf("admin", org.DefaultOrgID, org.RoleAdmin), true, false},
		{"a developer of org-sys is not the instance owner", memberOf("dev", org.DefaultOrgID, org.RoleDeveloper), true, false},
		{"an owner of another org is not the instance owner", memberOf("other", "acme", org.RoleOwner), true, false},
		{
			"an org-sys owner acting in another org is still the instance owner",
			access.Principal{
				IdentityID:  "other",
				Org:         "acme",
				Memberships: []org.Membership{{IdentityID: "other", OrgID: org.DefaultOrgID, Role: org.RoleOwner}},
			},
			true, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.has {
				ctx = access.WithPrincipal(ctx, tt.principal)
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/access/policies", nil)
			if got := instanceOwner(r); got != tt.want {
				t.Fatalf("instanceOwner = %v, want %v", got, tt.want)
			}
		})
	}
}
