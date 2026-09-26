package access

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

const acme = "acme"

func principal(role org.Role) access.Principal {
	return access.Principal{
		IdentityID: identity.IdentityID("11111111-1111-5111-8111-111111111111"),
		Kind:       identity.KindUser,
		Org:        acme,
		Memberships: []org.Membership{
			{IdentityID: identity.IdentityID("11111111-1111-5111-8111-111111111111"), OrgID: acme, Role: role},
		},
	}
}

func resource(kind access.ResourceKind, id string) access.Resource {
	return access.Resource{Kind: kind, ID: id, Org: acme, Workspace: "net"}
}

func newEngine(t *testing.T, policies ...access.Policy) *Engine {
	t.Helper()
	e, err := New(policies)
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	return e
}

func TestShippedRoleMatrix(t *testing.T) {
	shipped := access.ShippedOrgPolicies(acme)
	e := newEngine(t, shipped...)

	tests := []struct {
		name      string
		role      org.Role
		action    access.Action
		kind      access.ResourceKind
		id        string
		wantAllow bool
	}{
		// Viewer: read everything except secret values.
		{name: "viewer reads a binding", role: org.RoleViewer, action: access.ActionRead, kind: access.KindBinding, id: "b1", wantAllow: true},
		{name: "viewer reads logs", role: org.RoleViewer, action: access.ActionReadLogs, kind: access.KindTask, id: "1", wantAllow: true},
		{name: "viewer cannot read a secret", role: org.RoleViewer, action: access.ActionReadSecret, kind: access.KindSecret, id: "firewall-api"},
		{name: "viewer cannot create", role: org.RoleViewer, action: access.ActionCreate, kind: access.KindBinding, id: "b1"},
		{name: "viewer cannot run", role: org.RoleViewer, action: access.ActionRun, kind: access.KindWorkflow, id: "implement"},
		// Developer: create and edit, run, read logs -- no approve, no delete,
		// no secret values.
		{name: "developer creates a binding", role: org.RoleDeveloper, action: access.ActionCreate, kind: access.KindBinding, id: "b1", wantAllow: true},
		{name: "developer edits a mapping", role: org.RoleDeveloper, action: access.ActionUpdate, kind: access.KindMapping, id: "m1", wantAllow: true},
		{name: "developer runs a workflow", role: org.RoleDeveloper, action: access.ActionRun, kind: access.KindWorkflow, id: "implement", wantAllow: true},
		{name: "developer cannot approve", role: org.RoleDeveloper, action: access.ActionApprove, kind: access.KindBinding, id: "b1"},
		{name: "developer cannot delete", role: org.RoleDeveloper, action: access.ActionDelete, kind: access.KindBinding, id: "b1"},
		{name: "developer cannot read a secret", role: org.RoleDeveloper, action: access.ActionReadSecret, kind: access.KindSecret, id: "firewall-api"},
		{name: "developer cannot manage members", role: org.RoleDeveloper, action: access.ActionManageMembers, kind: access.KindMember, id: "u2"},
		// Admin: everything except deleting the org.
		{name: "admin approves a binding", role: org.RoleAdmin, action: access.ActionApprove, kind: access.KindBinding, id: "b1", wantAllow: true},
		{name: "admin deletes a binding", role: org.RoleAdmin, action: access.ActionDelete, kind: access.KindBinding, id: "b1", wantAllow: true},
		{name: "admin deletes the org is denied", role: org.RoleAdmin, action: access.ActionDelete, kind: access.KindOrg, id: acme},
		{name: "admin reads a secret", role: org.RoleAdmin, action: access.ActionReadSecret, kind: access.KindSecret, id: "firewall-api", wantAllow: true},
		{name: "admin manages policies", role: org.RoleAdmin, action: access.ActionManagePolicies, kind: access.KindPolicy, id: "p1", wantAllow: true},
		// Owner: everything in the org, including deleting it.
		{name: "owner deletes the org", role: org.RoleOwner, action: access.ActionDelete, kind: access.KindOrg, id: acme, wantAllow: true},
		{name: "owner manages identities", role: org.RoleOwner, action: access.ActionManageIdentities, kind: access.KindIdentity, id: "u2", wantAllow: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.Authorize(principal(tt.role), tt.action, resource(tt.kind, tt.id), access.Context{})
			if got.Allowed != tt.wantAllow {
				t.Fatalf("Authorize(%s, %s, %s/%s) allowed=%v level=%v policies=%v, want allow=%v",
					tt.role, tt.action, tt.kind, tt.id, got.Allowed, got.Level, got.Policies, tt.wantAllow)
			}
		})
	}
}

func TestCrossOrgForbid(t *testing.T) {
	e := newEngine(t, access.ShippedOrgPolicies(acme)...)
	p := access.Principal{
		IdentityID: identity.IdentityID("11111111-1111-5111-8111-111111111111"),
		Org:        acme,
		Memberships: []org.Membership{
			{IdentityID: identity.IdentityID("11111111-1111-5111-8111-111111111111"), OrgID: acme, Role: org.RoleOwner},
		},
	}
	got := e.Authorize(p, access.ActionRead, access.Resource{Kind: access.KindBinding, ID: "b1", Org: "other", Workspace: "net"}, access.Context{})
	if got.Allowed {
		t.Fatal("owner read of another org's binding was allowed; the cross-org forbid must win")
	}
	if got.Level != access.LevelInstance || len(got.Policies) != 1 || got.Policies[0] != access.CrossOrgForbidID {
		t.Fatalf("denial = level %v policies %v, want instance level with %q", got.Level, got.Policies, access.CrossOrgForbidID)
	}

	// An org-less principal is denied everything.
	agentless := p
	agentless.Org = ""
	if got := e.Authorize(agentless, access.ActionRead, resource(access.KindBinding, "b1"), access.Context{}); got.Allowed {
		t.Fatal("org-less principal was allowed")
	}

	// A create request with no resource org is the principal's own org.
	create := resource(access.KindBinding, "b1")
	create.Org = ""
	if got := e.Authorize(principal(org.RoleDeveloper), access.ActionCreate, create, access.Context{}); !got.Allowed {
		t.Fatalf("create in own org denied: level=%v policies=%v", got.Level, got.Policies)
	}
}

func TestWorkspacePolicyCannotWidenOrg(t *testing.T) {
	// The org forbids run where the workspace is production; a workspace
	// policy permitting run has no effect.
	orgPolicies := access.ShippedOrgPolicies(acme)
	orgPolicies = append(orgPolicies, access.Policy{
		ID: "org-no-run-production", Level: access.LevelOrg, OrgID: acme,
		Text: `forbid(principal, action == Archie::Action::"run", resource) when { resource.workspace == "production" };`,
	})
	workspace := access.Policy{
		ID: "ws-allow-run", Level: access.LevelWorkspace, OrgID: acme, WorkspaceID: "production",
		Text: `permit(principal, action, resource) when { action == Archie::Action::"run" };`,
	}
	e := newEngine(t, append(orgPolicies, workspace)...)
	r := resource(access.KindWorkflow, "implement")
	r.Workspace = "production"
	got := e.Authorize(principal(org.RoleDeveloper), access.ActionRun, r, access.Context{})
	if got.Allowed {
		t.Fatal("workspace policy widened an org forbid")
	}
	if got.Level != access.LevelOrg || got.Policies[0] != "org-no-run-production" {
		t.Fatalf("denial decided at %v by %v, want the org policy", got.Level, got.Policies)
	}
}

func TestObjectPolicyNarrows(t *testing.T) {
	// Only one identity may read the firewall-api secret.
	socID := identity.IdentityID("33333333-3333-5333-8333-333333333333")
	object := access.Policy{
		ID: "secret-soc-only", Level: access.LevelObject, OrgID: acme, WorkspaceID: "net",
		ObjectKind: access.KindSecret, ObjectID: "firewall-api",
		Text: `permit(principal, action == Archie::Action::"read_secret", resource) when {
    principal == Archie::Identity::"` + string(socID) + `"
};`,
	}
	e := newEngine(t, append(access.ShippedOrgPolicies(acme), object)...)

	// An admin would otherwise read it, but the object level narrows: the
	// object policy has no permit for this principal, so the level denies.
	admin := principal(org.RoleAdmin)
	admin.IdentityID = identity.IdentityID("22222222-2222-5222-8222-222222222222")
	got := e.Authorize(admin, access.ActionReadSecret, resource(access.KindSecret, "firewall-api"), access.Context{})
	if got.Allowed {
		t.Fatal("admin read a secret the object policy narrows away")
	}
	if got.Level != access.LevelObject {
		t.Fatalf("denial decided at %v, want object level", got.Level)
	}

	soc := admin
	soc.IdentityID = socID
	if got := e.Authorize(soc, access.ActionReadSecret, resource(access.KindSecret, "firewall-api"), access.Context{}); !got.Allowed {
		t.Fatalf("permitted identity denied: level=%v policies=%v", got.Level, got.Policies)
	}
}

func TestInvalidPolicyDeniesItsLevel(t *testing.T) {
	invalid := access.Policy{
		ID: "broken-workspace", Level: access.LevelWorkspace, OrgID: acme, WorkspaceID: "net",
		Text: `permit(principal, action, resource) when { principal.bogus == "x" };`,
	}
	e := newEngine(t, append(access.ShippedOrgPolicies(acme), invalid)...)
	problems := e.Problems()
	if len(problems) != 1 || problems[0].Policy.ID != "broken-workspace" {
		t.Fatalf("Problems() = %+v, want one entry for broken-workspace", problems)
	}
	if !errors.Is(problems[0].Err, access.ErrPolicyInvalidText) {
		t.Fatalf("problem err = %v, want ErrPolicyInvalidText", problems[0].Err)
	}
	// The workspace level denies everything, even what the org permits.
	got := e.Authorize(principal(org.RoleOwner), access.ActionRead, resource(access.KindBinding, "b1"), access.Context{})
	if got.Allowed || got.Level != access.LevelWorkspace {
		t.Fatalf("level with an invalid policy did not refuse: %+v", got)
	}
	if !errors.Is(got.Err, access.ErrPolicyInvalidText) {
		t.Fatalf("decision err = %v, want ErrPolicyInvalidText", got.Err)
	}
	// The denial record names the level's policies.
	if len(got.Policies) == 0 || got.Policies[0] != "broken-workspace" {
		t.Fatalf("refusal named %v, want the level's policy", got.Policies)
	}
}

func TestInvalidInstancePolicyStopsServing(t *testing.T) {
	if _, err := New([]access.Policy{
		{ID: "broken-instance", Level: access.LevelInstance, Text: `permit(principal, action, resource) when { resource.nope == 1 };`},
	}); err == nil {
		t.Fatal("New() accepted an invalid instance policy; archie must stop serving")
	}
}

func TestValidateRefusesUnknownVocabulary(t *testing.T) {
	e := newEngine(t)
	bad := access.Policy{ID: "p1", Level: access.LevelOrg, OrgID: acme, Text: `permit(principal, action, resource) when { resource.wat == "x" };`}
	if err := e.Validate(bad); !errors.Is(err, access.ErrPolicyInvalidText) {
		t.Fatalf("Validate(unknown attribute) = %v, want ErrPolicyInvalidText", err)
	}
	badText := access.Policy{ID: "p2", Level: access.LevelOrg, OrgID: acme, Text: `permit(principal, action, resource) when { principal.org == 42 };`}
	if err := e.Validate(badText); !errors.Is(err, access.ErrPolicyInvalidText) {
		t.Fatalf("Validate(type error) = %v, want ErrPolicyInvalidText", err)
	}
	multi := access.Policy{ID: "p3", Level: access.LevelOrg, OrgID: acme, Text: `permit(principal, action, resource); permit(principal, action, resource);`}
	if err := e.Validate(multi); !errors.Is(err, access.ErrPolicyInvalidText) {
		t.Fatalf("Validate(multiple policies) = %v, want ErrPolicyInvalidText", err)
	}
}

func TestWorkspaceRoleNarrowsTheOrgRole(t *testing.T) {
	// A developer org-wide, viewer in the net workspace: read-only there.
	p := principal(org.RoleDeveloper)
	p.Memberships = append(p.Memberships, org.Membership{IdentityID: p.IdentityID, OrgID: acme, WorkspaceID: "net", Role: org.RoleViewer})
	e := newEngine(t, access.ShippedOrgPolicies(acme)...)
	if got := e.Authorize(p, access.ActionRead, resource(access.KindBinding, "b1"), access.Context{}); !got.Allowed {
		t.Fatalf("viewer-in-workspace denied a read: %+v", got)
	}
	if got := e.Authorize(p, access.ActionCreate, resource(access.KindBinding, "b1"), access.Context{}); got.Allowed {
		t.Fatal("workspace viewer role did not narrow the org-wide developer role")
	}
}

func TestDenialNamesDecidingPolicies(t *testing.T) {
	e := newEngine(t, access.ShippedOrgPolicies(acme)...)
	// A viewer denied create is decided by the org level, whose only
	// policies are the shipped role set.
	got := e.Authorize(principal(org.RoleViewer), access.ActionCreate, resource(access.KindBinding, "b1"), access.Context{})
	if got.Level != access.LevelOrg {
		t.Fatalf("level = %v, want org", got.Level)
	}
	for _, id := range got.Policies {
		if id != access.PolicyOrgRead && id != access.PolicyOrgEdit && id != access.PolicyOrgAdmin && id != access.PolicyOrgOwner {
			t.Fatalf("denial names an unknown policy %q", id)
		}
	}
}
