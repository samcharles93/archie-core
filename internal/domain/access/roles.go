// The shipped policies: the four org roles every org carries, and the
// cross-org forbid the engine always enforces
// (docs/prds/orgs-and-access.md, "The policy chain" and "Roles").
//
// The role policies are Cedar policy text because that is what the engine
// evaluates. They live in the domain so the store seeds them, the reset
// command restores them, and the engine ships the same words regardless of
// which process builds it.
package access

import "github.com/samcharles93/archie-core/internal/domain/org"

// Role names as the shipped role policies test them: org.Role's lowercase
// vocabulary, carried in the principal's `role` attribute.
const (
	roleOwner     string = string(org.RoleOwner)
	roleAdmin     string = string(org.RoleAdmin)
	roleDeveloper string = string(org.RoleDeveloper)
	roleViewer    string = string(org.RoleViewer)
)

// Shipped policy IDs. A stored policy must not reuse one: the store's
// seeding and the reset command overwrite these IDs with the shipped text.
const (
	PolicyOrgRead    = "org-role-read"
	PolicyOrgEdit    = "org-role-edit"
	PolicyOrgAdmin   = "org-role-admin"
	PolicyOrgOwner   = "org-role-owner"
	PolicyCrossOrgID = "instance-cross-org"
)

// orgReadPolicy is every role's floor: read everything in the org, and its
// logs, but not a secret's value.
const orgReadPolicy = `permit(principal, action, resource) when {
    resource.org == principal.org &&
    ["` + roleViewer + `", "` + roleDeveloper + `", "` + roleAdmin + `", "` + roleOwner + `"].contains(principal.role) &&
    action in [Archie::Action::"read", Archie::Action::"read_logs"]
};`

// orgEditPolicy is a developer's grant: create and edit workspace resources
// and org workflows, and run them.
const orgEditPolicy = `permit(principal, action, resource) when {
    resource.org == principal.org &&
    ["` + roleDeveloper + `", "` + roleAdmin + `", "` + roleOwner + `"].contains(principal.role) &&
    action in [Archie::Action::"create", Archie::Action::"update", Archie::Action::"run"]
};`

// orgAdminPolicy is an admin's grant: approve, secret values, member and
// identity management, policy management, and deletion of every record that
// is not the org itself.
const orgAdminPolicy = `permit(principal, action, resource) when {
    resource.org == principal.org &&
    ["` + roleAdmin + `", "` + roleOwner + `"].contains(principal.role) &&
    (action != Archie::Action::"delete" || resource.kind != "org")
};`

// orgOwnerPolicy is the owner's grant: everything in the org, including
// deleting it.
const orgOwnerPolicy = `permit(principal, action, resource) when {
    resource.org == principal.org &&
    principal.role == "` + roleOwner + `"
};`

// ShippedOrgPolicies returns the four shipped role policies for one org, as
// the store seeds them and `archied access reset --org` restores them.
func ShippedOrgPolicies(orgID org.OrgID) []Policy {
	return []Policy{
		{ID: PolicyOrgRead, Level: LevelOrg, OrgID: orgID, Text: orgReadPolicy},
		{ID: PolicyOrgEdit, Level: LevelOrg, OrgID: orgID, Text: orgEditPolicy},
		{ID: PolicyOrgAdmin, Level: LevelOrg, OrgID: orgID, Text: orgAdminPolicy},
		{ID: PolicyOrgOwner, Level: LevelOrg, OrgID: orgID, Text: orgOwnerPolicy},
	}
}

// CrossOrgForbidID is the ID the shipped cross-org forbid is known by in
// denial records. The forbid is engine-enforced, never stored, so it cannot
// be edited away or removed by `archied access reset --instance`.
const CrossOrgForbidID = PolicyCrossOrgID

// The cross-org forbid's semantics, carried as text for the policy surface
// and the reset command's audit trail. The engine enforces it structurally
// (a principal never reaches a resource outside its org) before any Cedar
// runs, which is what makes it non-removable.
const CrossOrgForbidText = `forbid(principal, action, resource) when {
    resource.org != principal.org;
};`
