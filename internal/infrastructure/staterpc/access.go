// Access: the policy-chain and denial-record RPCs (docs/prds/orgs-and-access.md).
// The policies surface is the engine's snapshot plus the administrative
// writes; the denials surface is the two callers' write path and the
// operator's read.
package staterpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

var (
	errPoliciesUnavailable = status.Error(codes.Unavailable, "access policy store unavailable")
	errDenialsUnavailable  = status.Error(codes.Unavailable, "access denial store unavailable")
)

func accessPolicyProto(p access.Policy) *pb.AccessPolicy {
	return &pb.AccessPolicy{
		PolicyId: p.ID, Level: string(p.Level), OrgId: string(p.OrgID),
		WorkspaceId: string(p.WorkspaceID), ObjectKind: string(p.ObjectKind),
		ObjectId: p.ObjectID, Text: p.Text,
	}
}

func accessPolicyValue(p *pb.AccessPolicy) access.Policy {
	return access.Policy{
		ID: p.GetPolicyId(), Level: access.Level(p.GetLevel()),
		OrgID: org.OrgID(p.GetOrgId()), WorkspaceID: org.WorkspaceID(p.GetWorkspaceId()),
		ObjectKind: access.ResourceKind(p.GetObjectKind()), ObjectID: p.GetObjectId(),
		Text: p.GetText(),
	}
}

func accessDenialProto(d access.Denial) *pb.AccessDenial {
	out := &pb.AccessDenial{
		OrgId: string(d.Org), Principal: string(d.Principal), Action: string(d.Action),
		ResourceKind: string(d.Kind), ResourceId: d.ResourceID, Level: string(d.Level),
		Policies: d.Policies, Count: d.Count,
	}
	if !d.At.IsZero() {
		out.At = timestamp(d.At)
	}
	return out
}

func accessDenialValue(d *pb.AccessDenial) access.Denial {
	value := access.Denial{
		Org:        org.OrgID(d.GetOrgId()),
		Principal:  identity.IdentityID(d.GetPrincipal()),
		Action:     access.Action(d.GetAction()),
		Kind:       access.ResourceKind(d.GetResourceKind()),
		ResourceID: d.GetResourceId(),
		Level:      access.Level(d.GetLevel()),
		Policies:   d.GetPolicies(),
		Count:      d.GetCount(),
	}
	if d.GetAt() != nil {
		value.At = d.GetAt().AsTime()
	}
	return value
}

func (s *server) GetPrincipal(ctx context.Context, request *pb.GetPrincipalRequest) (*pb.GetPrincipalResponse, error) {
	if s.deps.Principals == nil {
		return nil, status.Error(codes.Unavailable, "identity store unavailable")
	}
	value, err := s.deps.Principals.PrincipalFor(ctx, identity.IdentityID(request.IdentityId))
	if err != nil {
		return nil, identityStatus(err)
	}
	memberships := make([]*pb.PrincipalMembership, len(value.Memberships))
	for i := range value.Memberships {
		m := value.Memberships[i]
		memberships[i] = &pb.PrincipalMembership{
			OrgId: string(m.OrgID), WorkspaceId: string(m.WorkspaceID), Role: string(m.Role),
		}
	}
	return &pb.GetPrincipalResponse{Principal: &pb.AccessPrincipal{
		IdentityId:  string(value.IdentityID),
		OrgId:       string(value.Org),
		Memberships: memberships,
	}}, nil
}

func (s *server) ListPolicies(ctx context.Context, _ *pb.ListPoliciesRequest) (*pb.ListPoliciesResponse, error) {
	if s.deps.Policies == nil {
		return nil, errPoliciesUnavailable
	}
	values, err := s.deps.Policies.ListPolicies(ctx)
	if err != nil {
		return nil, accessStatus(err)
	}
	out := make([]*pb.AccessPolicy, len(values))
	for i := range values {
		out[i] = accessPolicyProto(values[i])
	}
	return &pb.ListPoliciesResponse{Policies: out}, nil
}

func (s *server) PutPolicy(ctx context.Context, request *pb.PutPolicyRequest) (*pb.PutPolicyResponse, error) {
	if s.deps.Policies == nil {
		return nil, errPoliciesUnavailable
	}
	version, err := s.deps.Policies.PutPolicy(ctx, accessPolicyValue(request.Policy))
	if err != nil {
		return nil, accessStatus(err)
	}
	return &pb.PutPolicyResponse{Version: version}, nil
}

func (s *server) DeletePolicy(ctx context.Context, request *pb.DeletePolicyRequest) (*pb.DeletePolicyResponse, error) {
	if s.deps.Policies == nil {
		return nil, errPoliciesUnavailable
	}
	if err := s.deps.Policies.DeletePolicy(ctx, accessPolicyValue(request.Policy)); err != nil {
		return nil, accessStatus(err)
	}
	return &pb.DeletePolicyResponse{}, nil
}

func (s *server) EnsureShippedOrgPolicies(ctx context.Context, request *pb.EnsureShippedOrgPoliciesRequest) (*pb.EnsureShippedOrgPoliciesResponse, error) {
	if s.deps.Policies == nil {
		return nil, errPoliciesUnavailable
	}
	if err := s.deps.Policies.EnsureShippedOrgPolicies(ctx, org.OrgID(request.OrgId)); err != nil {
		return nil, accessStatus(err)
	}
	return &pb.EnsureShippedOrgPoliciesResponse{}, nil
}

func (s *server) RecordDenial(ctx context.Context, request *pb.RecordDenialRequest) (*pb.RecordDenialResponse, error) {
	if s.deps.Denials == nil {
		return nil, errDenialsUnavailable
	}
	if err := s.deps.Denials.RecordDenial(ctx, accessDenialValue(request.Denial)); err != nil {
		return nil, accessStatus(err)
	}
	return &pb.RecordDenialResponse{}, nil
}

func (s *server) ListDenials(ctx context.Context, request *pb.ListDenialsRequest) (*pb.ListDenialsResponse, error) {
	if s.deps.Denials == nil {
		return nil, errDenialsUnavailable
	}
	limit := int(request.GetLimit())
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	values, err := s.deps.Denials.ListDenials(ctx, org.OrgID(request.OrgId), limit)
	if err != nil {
		return nil, accessStatus(err)
	}
	out := make([]*pb.AccessDenial, len(values))
	for i := range values {
		out[i] = accessDenialProto(values[i])
	}
	return &pb.ListDenialsResponse{Denials: out}, nil
}

// accessStatus maps an access sentinel to its gRPC status.
func accessStatus(err error) error {
	if errors.Is(err, access.ErrPolicyNotFound) {
		return status.Error(codes.NotFound, access.ErrPolicyNotFound.Error())
	}
	if errors.Is(err, access.ErrInvalidPolicy) {
		return status.Error(codes.InvalidArgument, access.ErrInvalidPolicy.Error())
	}
	return mapError(err)
}
