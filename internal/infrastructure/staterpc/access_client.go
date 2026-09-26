// Access: the client half of the policy-chain and denial-record RPCs
// (docs/prds/orgs-and-access.md). The reset surface is deliberately absent:
// `archied access reset` runs on the State Store host, never over the wire.
package staterpc

import (
	"context"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// The Client satisfies the access contracts the two callers depend on.
var (
	_ access.PolicyStore     = (*Client)(nil)
	_ access.DenialStore     = (*Client)(nil)
	_ access.PrincipalSource = (*Client)(nil)
)

func (c *Client) PrincipalFor(ctx context.Context, id identity.IdentityID) (access.Principal, error) {
	r, err := c.client.GetPrincipal(ctx, &pb.GetPrincipalRequest{IdentityId: string(id)})
	if err != nil {
		return access.Principal{}, unmapError(err)
	}
	value := r.GetPrincipal()
	memberships := make([]org.Membership, len(value.GetMemberships()))
	for i := range value.GetMemberships() {
		m := value.GetMemberships()[i]
		memberships[i] = org.Membership{
			IdentityID:  id,
			OrgID:       org.OrgID(m.GetOrgId()),
			WorkspaceID: org.WorkspaceID(m.GetWorkspaceId()),
			Role:        org.Role(m.GetRole()),
		}
	}
	return access.Principal{
		IdentityID:  identity.IdentityID(value.GetIdentityId()),
		Org:         org.OrgID(value.GetOrgId()),
		Memberships: memberships,
	}, nil
}

func (c *Client) ListPolicies(ctx context.Context) ([]access.Policy, error) {
	r, err := c.client.ListPolicies(ctx, &pb.ListPoliciesRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	out := make([]access.Policy, len(r.Policies))
	for i := range r.Policies {
		out[i] = accessPolicyValue(r.Policies[i])
	}
	return out, nil
}

func (c *Client) PutPolicy(ctx context.Context, p access.Policy) (int64, error) {
	r, err := c.client.PutPolicy(ctx, &pb.PutPolicyRequest{Policy: accessPolicyProto(p)})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Version, nil
}

func (c *Client) DeletePolicy(ctx context.Context, p access.Policy) error {
	_, err := c.client.DeletePolicy(ctx, &pb.DeletePolicyRequest{Policy: accessPolicyProto(p)})
	return unmapError(err)
}

func (c *Client) EnsureShippedOrgPolicies(ctx context.Context, orgID org.OrgID) error {
	_, err := c.client.EnsureShippedOrgPolicies(ctx, &pb.EnsureShippedOrgPoliciesRequest{OrgId: string(orgID)})
	return unmapError(err)
}

func (c *Client) RecordDenial(ctx context.Context, d access.Denial) error {
	_, err := c.client.RecordDenial(ctx, &pb.RecordDenialRequest{Denial: accessDenialProto(d)})
	return unmapError(err)
}

func (c *Client) ListDenials(ctx context.Context, orgID org.OrgID, limit int) ([]access.Denial, error) {
	r, err := c.client.ListDenials(ctx, &pb.ListDenialsRequest{OrgId: string(orgID), Limit: int32(limit)})
	if err != nil {
		return nil, unmapError(err)
	}
	out := make([]access.Denial, len(r.Denials))
	for i := range r.Denials {
		out[i] = accessDenialValue(r.Denials[i])
	}
	return out, nil
}
