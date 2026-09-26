package access

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

func TestPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		wantErr error
	}{
		{
			name:   "instance policy with no scope",
			policy: Policy{ID: "p1", Level: LevelInstance, Text: "permit(principal, action, resource);"},
		},
		{
			name:    "instance policy without text",
			policy:  Policy{ID: "p1", Level: LevelInstance},
			wantErr: ErrInvalidPolicy,
		},
		{
			name:    "org policy without org",
			policy:  Policy{ID: "p1", Level: LevelOrg, Text: "permit(principal, action, resource);"},
			wantErr: ErrInvalidPolicy,
		},
		{
			name:    "workspace policy without workspace",
			policy:  Policy{ID: "p1", Level: LevelWorkspace, OrgID: "acme", Text: "permit(principal, action, resource);"},
			wantErr: ErrInvalidPolicy,
		},
		{
			name: "object policy with full scope",
			policy: Policy{
				ID: "p1", Level: LevelObject, OrgID: "acme", WorkspaceID: "net",
				ObjectKind: KindSecret, ObjectID: "firewall-api",
				Text: "forbid(principal, action == Archie::Action::\"read_secret\", resource);",
			},
		},
		{
			name:    "object policy without kind",
			policy:  Policy{ID: "p1", Level: LevelObject, OrgID: "acme", WorkspaceID: "net", ObjectID: "firewall-api", Text: "forbid(principal, action, resource);"},
			wantErr: ErrInvalidPolicy,
		},
		{
			name:    "unknown level",
			policy:  Policy{ID: "p1", Level: "galaxy", Text: "permit(principal, action, resource);"},
			wantErr: ErrInvalidLevel,
		},
		{
			name:    "no ID",
			policy:  Policy{Level: LevelInstance, Text: "permit(principal, action, resource);"},
			wantErr: ErrInvalidPolicy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPolicyGoverns(t *testing.T) {
	res := Resource{Kind: KindBinding, ID: "b1", Org: "acme", Workspace: "net"}

	tests := []struct {
		name   string
		policy Policy
		want   bool
	}{
		{
			name:   "instance governs everything",
			policy: Policy{Level: LevelInstance},
			want:   true,
		},
		{
			name:   "org policy for the same org",
			policy: Policy{Level: LevelOrg, OrgID: "acme"},
			want:   true,
		},
		{
			name:   "org policy for another org never governs",
			policy: Policy{Level: LevelOrg, OrgID: "other"},
			want:   false,
		},
		{
			name:   "workspace policy for the resource's workspace",
			policy: Policy{Level: LevelWorkspace, OrgID: "acme", WorkspaceID: "net"},
			want:   true,
		},
		{
			name:   "workspace policy for another workspace",
			policy: Policy{Level: LevelWorkspace, OrgID: "acme", WorkspaceID: "desk"},
			want:   false,
		},
		{
			name:   "object policy for the same object",
			policy: Policy{Level: LevelObject, OrgID: "acme", WorkspaceID: "net", ObjectKind: KindBinding, ObjectID: "b1"},
			want:   true,
		},
		{
			name:   "object policy for another object",
			policy: Policy{Level: LevelObject, OrgID: "acme", WorkspaceID: "net", ObjectKind: KindBinding, ObjectID: "b2"},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.Governs(res); got != tt.want {
				t.Fatalf("Governs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrincipalRole(t *testing.T) {
	id := identity.IdentityID("11111111-1111-5111-8111-111111111111")
	p := Principal{
		IdentityID: id,
		Org:        "acme",
		Memberships: []org.Membership{
			{IdentityID: id, OrgID: "acme", Role: org.RoleDeveloper},
			{IdentityID: id, OrgID: "acme", WorkspaceID: "net", Role: org.RoleViewer},
			{IdentityID: id, OrgID: "other", Role: org.RoleOwner}, // not this org's
		},
	}

	tests := []struct {
		name string
		ws   org.WorkspaceID
		want org.Role
	}{
		{name: "workspace membership wins", ws: "net", want: org.RoleViewer},
		{name: "org-wide role otherwise", ws: "desk", want: org.RoleDeveloper},
		{name: "org-wide role with no workspace", ws: "", want: org.RoleDeveloper},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Role(tt.ws); got != tt.want {
				t.Fatalf("Role(%q) = %q, want %q", tt.ws, got, tt.want)
			}
		})
	}

	noRole := Principal{IdentityID: id, Org: "acme"}
	if got := noRole.Role("net"); got != "" {
		t.Fatalf("roleless principal Role() = %q, want empty", got)
	}
}

func TestActionValidate(t *testing.T) {
	for _, a := range Actions() {
		if err := a.Validate(); err != nil {
			t.Fatalf("Action(%q).Validate() = %v, want nil", a, err)
		}
	}
	if err := Action("reboot").Validate(); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("unknown action Validate() = %v, want ErrInvalidAction", err)
	}
}

func TestShippedOrgPolicies(t *testing.T) {
	policies := ShippedOrgPolicies("acme")
	if len(policies) != 4 {
		t.Fatalf("len(ShippedOrgPolicies) = %d, want 4", len(policies))
	}
	for _, p := range policies {
		if p.Level != LevelOrg || p.OrgID != "acme" {
			t.Fatalf("%s: level %q org %q, want org-level policy for acme", p.ID, p.Level, p.OrgID)
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: Validate() = %v, want nil", p.ID, err)
		}
	}
	ids := make(map[string]bool, len(policies))
	for _, p := range policies {
		ids[p.ID] = true
	}
	for _, want := range []string{PolicyOrgRead, PolicyOrgEdit, PolicyOrgAdmin, PolicyOrgOwner} {
		if !ids[want] {
			t.Fatalf("ShippedOrgPolicies missing %q", want)
		}
	}
}
