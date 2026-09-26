// Package org owns the tenant boundary: orgs, the workspaces that divide them,
// and the memberships that give an identity a role in either
// (docs/prds/orgs-and-access.md).
package org

import (
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// OrgID and WorkspaceID identify an org and a workspace.
type (
	OrgID       string
	WorkspaceID string
)

// The org and workspace every record of a single-operator install, and every
// record written before orgs existed, belongs to.
const (
	DefaultOrgID       OrgID       = "default"
	DefaultWorkspaceID WorkspaceID = "default"
)

var (
	ErrInvalidRole       = errors.New("org: invalid role")
	ErrInvalidOrg        = errors.New("org: invalid org")
	ErrInvalidMembership = errors.New("org: invalid membership")
	ErrInvalidWorkspace  = errors.New("org: invalid workspace")
	ErrInvalidAssignment = errors.New("org: invalid agent assignment")
	ErrOrgNotFound       = errors.New("org: org not found")
	ErrOrgExists         = errors.New("org: org already exists")
	ErrWorkspaceExists   = errors.New("org: workspace already exists")
	ErrMembershipExists  = errors.New("org: membership already exists")
	// ErrUpgradeIncomplete reports that the resumable default org/workspace
	// upgrade has not finished (docs/prds/orgs-and-access.md, "Upgrading
	// existing installs"): the store refuses scoped calls rather than serving
	// records with no org.
	ErrUpgradeIncomplete = errors.New("org: default org upgrade incomplete")
)

// Role is a member's role in an org or one of its workspaces.
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// Validate reports whether r is one of the shipped roles.
func (r Role) Validate() error {
	switch r {
	case RoleOwner, RoleAdmin, RoleDeveloper, RoleViewer:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidRole, r)
}

// Org is a tenant.
type Org struct {
	ID   OrgID  `json:"id"`
	Name string `json:"name"`
}

// Validate requires an org to have an ID and a name.
func (o Org) Validate() error {
	if o.ID == "" || strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("%w: ID and name are required", ErrInvalidOrg)
	}
	return nil
}

// Workspace divides an org. Environment is a free-form attribute policies can
// test; it is not required.
type Workspace struct {
	ID          WorkspaceID `json:"id"`
	OrgID       OrgID       `json:"org_id"`
	Name        string      `json:"name"`
	Environment string      `json:"environment,omitempty"`
}

// Validate requires a workspace to belong to an org and to have a name.
func (w Workspace) Validate() error {
	if w.OrgID == "" {
		return fmt.Errorf("%w: no org", ErrInvalidWorkspace)
	}
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("%w: no name", ErrInvalidWorkspace)
	}
	return nil
}

// Membership gives an identity a role in an org, or in one workspace of it
// when WorkspaceID is set.
type Membership struct {
	IdentityID  identity.IdentityID `json:"identity_id"`
	OrgID       OrgID               `json:"org_id"`
	WorkspaceID WorkspaceID         `json:"workspace_id,omitempty"`
	Role        Role                `json:"role"`
}

// Validate requires an identity, an org and a shipped role.
func (m Membership) Validate() error {
	if m.IdentityID == "" {
		return fmt.Errorf("%w: no identity", ErrInvalidMembership)
	}
	if m.OrgID == "" {
		return fmt.Errorf("%w: no org", ErrInvalidMembership)
	}
	return m.Role.Validate()
}

// AgentAssignment records the one org an agent or service identity serves.
// People join orgs through memberships; agents are assigned to exactly one.
type AgentAssignment struct {
	IdentityID identity.IdentityID `json:"identity_id"`
	OrgID      OrgID               `json:"org_id"`
}

// Validate requires an identity and an org.
func (a AgentAssignment) Validate() error {
	if a.IdentityID == "" || a.OrgID == "" {
		return fmt.Errorf("%w: identity and org are required", ErrInvalidAssignment)
	}
	return nil
}
