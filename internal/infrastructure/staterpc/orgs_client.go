package staterpc

import (
	"context"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// The Client serves the dashboard's org surface over the contract.
var _ org.API = (*Client)(nil)

func (c *Client) CreateOrg(ctx context.Context, value org.Org, owner identity.IdentityID) (org.Org, error) {
	r, err := c.client.CreateOrg(ctx, &pb.CreateOrgRequest{Id: string(value.ID), Name: value.Name, OwnerIdentityId: string(owner)})
	if err != nil {
		return org.Org{}, unmapError(err)
	}
	return orgValue(r.Org), nil
}

func (c *Client) GetOrg(ctx context.Context, id org.OrgID) (org.Org, error) {
	r, err := c.client.GetOrg(ctx, &pb.GetOrgRequest{OrgId: string(id)})
	if err != nil {
		return org.Org{}, unmapError(err)
	}
	return orgValue(r.Org), nil
}

func (c *Client) ListOrgs(ctx context.Context) ([]org.Org, error) {
	r, err := c.client.ListOrgs(ctx, &pb.ListOrgsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Orgs, orgValue), nil
}

func (c *Client) CreateWorkspace(ctx context.Context, value org.Workspace) (org.Workspace, error) {
	r, err := c.client.CreateWorkspace(ctx, &pb.CreateWorkspaceRequest{
		OrgId: string(value.OrgID), Id: string(value.ID), Name: value.Name, Environment: value.Environment,
	})
	if err != nil {
		return org.Workspace{}, unmapError(err)
	}
	return workspaceValue(r.Workspace), nil
}

func (c *Client) ListWorkspaces(ctx context.Context, id org.OrgID) ([]org.Workspace, error) {
	r, err := c.client.ListWorkspaces(ctx, &pb.ListWorkspacesRequest{OrgId: string(id)})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Workspaces, workspaceValue), nil
}

func (c *Client) EnsureMembership(ctx context.Context, value org.Membership) error {
	_, err := c.client.SetMembership(ctx, &pb.SetMembershipRequest{
		OrgId: string(value.OrgID), IdentityId: string(value.IdentityID),
		WorkspaceId: string(value.WorkspaceID), Role: string(value.Role),
	})
	return unmapError(err)
}

func (c *Client) ListMembers(ctx context.Context, id org.OrgID) ([]org.Member, error) {
	r, err := c.client.ListMembers(ctx, &pb.ListMembersRequest{OrgId: string(id)})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Members, func(m *pb.Member) org.Member {
		return org.Member{
			IdentityID: identity.IdentityID(m.GetIdentityId()), Kind: identity.Kind(m.GetKind()),
			DisplayName: m.GetDisplayName(), WorkspaceID: org.WorkspaceID(m.GetWorkspaceId()), Role: org.Role(m.GetRole()),
		}
	}), nil
}

func (c *Client) RemoveMembership(ctx context.Context, value org.Membership) error {
	_, err := c.client.RemoveMembership(ctx, &pb.RemoveMembershipRequest{
		OrgId: string(value.OrgID), IdentityId: string(value.IdentityID), WorkspaceId: string(value.WorkspaceID),
	})
	return unmapError(err)
}

func (c *Client) AssignAgent(ctx context.Context, value org.AgentAssignment) error {
	_, err := c.client.AssignAgent(ctx, &pb.AssignAgentRequest{OrgId: string(value.OrgID), IdentityId: string(value.IdentityID)})
	return unmapError(err)
}
