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
type Repository interface {
	// CreateOrg inserts a new org; re-creating an existing ID is an error.
	CreateOrg(context.Context, Org) (Org, error)
	// GetOrg returns one org by ID, or ErrOrgNotFound.
	GetOrg(context.Context, OrgID) (Org, error)
	// ListOrgs returns every org, oldest first.
	ListOrgs(context.Context) ([]Org, error)
	// CreateWorkspace inserts a workspace into an existing org.
	CreateWorkspace(context.Context, Workspace) (Workspace, error)
	// ListWorkspaces returns an org's workspaces, oldest first.
	ListWorkspaces(context.Context, OrgID) ([]Workspace, error)
	// EnsureMembership grants an identity a role in an org, or in one
	// workspace of it; an identical existing membership is not an error.
	EnsureMembership(context.Context, Membership) error
	// AssignAgent records the one org an agent or service identity serves,
	// replacing any prior assignment for that identity.
	AssignAgent(context.Context, AgentAssignment) error
	// OrgForIdentity returns the org an identity serves: the assigned org
	// for an agent, or the org of one of its memberships for a person.
	// An identity in no org resolves to DefaultOrgID.
	OrgForIdentity(context.Context, identity.IdentityID) (OrgID, error)
}

// Upgrader performs the resumable default org/workspace upgrade
// (docs/prds/orgs-and-access.md, "Upgrading existing installs"). It runs in
// the State Store first, then in the event store, each phase resumable from
// its recorded ledger row, and it must run before scoped calls are served.
type Upgrader interface {
	// UpgradeDefaultOrg is idempotent: a completed phase is never re-run,
	// an interrupted one resumes from where the ledger says it stopped.
	UpgradeDefaultOrg(context.Context) error
}
