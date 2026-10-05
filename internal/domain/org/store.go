// The persistence contract for orgs, workspaces, memberships and agent
// assignments. Postgres implements it (internal/infrastructure/postgres);
// composition wires it like identity.Repository.
package org

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// Repository persists the tenant boundary. A store that implements it owns
// ID conflict rules (org and workspace IDs are caller-provided and unique).
type Repository interface { //nolint:interfacebloat // one tenant-boundary store contract: orgs, workspaces, memberships and agent assignments
	// CreateOrg inserts a new org with its default workspace and owner;
	// re-creating an existing ID is an error.
	CreateOrg(context.Context, Org, identity.IdentityID) (Org, error)
	// GetOrg returns one org by ID, or ErrOrgNotFound.
	GetOrg(context.Context, OrgID) (Org, error)
	// ListOrgs returns every org, oldest first.
	ListOrgs(context.Context) ([]Org, error)
	// CreateWorkspace inserts a workspace into an existing org.
	CreateWorkspace(context.Context, Workspace) (Workspace, error)
	// ListWorkspaces returns an org's workspaces, oldest first.
	ListWorkspaces(context.Context, OrgID) ([]Workspace, error)
	// EnsureMembership grants an identity a role in an org, or in one
	// workspace of it, changing the role of an existing membership; an
	// identical existing membership is not an error.
	EnsureMembership(context.Context, Membership) error
	// ListMembers returns an org's memberships with the identity each names.
	ListMembers(context.Context, OrgID) ([]Member, error)
	// RemoveMembership revokes one membership; a missing one is not an error.
	RemoveMembership(context.Context, Membership) error
	// ListOrgAgents returns the agent and service identities assigned to an org.
	ListOrgAgents(context.Context, OrgID) ([]AgentAssignment, error)
	// AssignAgent records the one org an agent or service identity serves,
	// replacing any prior assignment for that identity.
	AssignAgent(context.Context, AgentAssignment) error
	// OrgForIdentity returns the org an identity serves: the assigned org
	// for an agent, or the org of one of its memberships for a person.
	// An identity in no org resolves to DefaultOrgID.
	OrgForIdentity(context.Context, identity.IdentityID) (OrgID, error)
}

// API is the org surface a remote caller uses: the subset of Repository the
// State Store contract carries. It is the dashboard's read and write surface;
// the assignment list has no RPC and stays off it.
type API interface {
	ListOrgs(context.Context) ([]Org, error)
	GetOrg(context.Context, OrgID) (Org, error)
	ListWorkspaces(context.Context, OrgID) ([]Workspace, error)
	CreateWorkspace(context.Context, Workspace) (Workspace, error)
	ListMembers(context.Context, OrgID) ([]Member, error)
	EnsureMembership(context.Context, Membership) error
	RemoveMembership(context.Context, Membership) error
	AssignAgent(context.Context, AgentAssignment) error
}

// Creator makes an org with its default workspace and first owner, the one
// instance-admin write the dashboard carries.
type Creator interface {
	CreateOrg(context.Context, Org, identity.IdentityID) (Org, error)
}

// Upgrader performs the resumable default org/workspace upgrade.
// It runs in
// the State Store first, then in the event store, each phase resumable from
// its recorded ledger row, and it must run before scoped calls are served.
type Upgrader interface {
	// UpgradeDefaultOrg is idempotent: a completed phase is never re-run,
	// an interrupted one resumes from where the ledger says it stopped.
	UpgradeDefaultOrg(context.Context) error
}
