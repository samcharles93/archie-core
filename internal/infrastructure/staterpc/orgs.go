package staterpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// errOrgsUnavailable reports that this process hosts no org store.
var errOrgsUnavailable = status.Error(codes.Unavailable, "org store unavailable")

// orgStatus maps the org domain's errors onto the wire, keeping the message
// that names the offending role, org or workspace.
func orgStatus(err error) error {
	switch {
	case errors.Is(err, org.ErrOrgNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, org.ErrOrgExists), errors.Is(err, org.ErrWorkspaceExists), errors.Is(err, org.ErrMembershipExists):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, org.ErrInvalidRole), errors.Is(err, org.ErrInvalidOrg),
		errors.Is(err, org.ErrInvalidMembership), errors.Is(err, org.ErrInvalidWorkspace),
		errors.Is(err, org.ErrInvalidAssignment), errors.Is(err, org.ErrUpgradeIncomplete):
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return mapError(err)
}

// orgErr logs and maps one org RPC's failure.
func (s *server) orgErr(rpc string, err error) error {
	if err != nil {
		s.deps.Log.Error("staterpc call failed", "rpc", rpc, "err", err)
	}
	return orgStatus(err)
}

func orgProto(o org.Org) *pb.Org { return &pb.Org{Id: string(o.ID), Name: o.Name} }

func orgValue(o *pb.Org) org.Org { return org.Org{ID: org.OrgID(o.GetId()), Name: o.GetName()} }

func workspaceProto(w org.Workspace) *pb.Workspace {
	return &pb.Workspace{Id: string(w.ID), OrgId: string(w.OrgID), Name: w.Name, Environment: w.Environment}
}

func workspaceValue(w *pb.Workspace) org.Workspace {
	return org.Workspace{
		ID: org.WorkspaceID(w.GetId()), OrgID: org.OrgID(w.GetOrgId()),
		Name: w.GetName(), Environment: w.GetEnvironment(),
	}
}

func memberProto(m org.Member) *pb.Member {
	return &pb.Member{
		IdentityId: string(m.IdentityID), Kind: string(m.Kind), DisplayName: m.DisplayName,
		WorkspaceId: string(m.WorkspaceID), Role: string(m.Role),
	}
}

func (s *server) ListOrgs(ctx context.Context, _ *pb.ListOrgsRequest) (*pb.ListOrgsResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	values, err := s.deps.Orgs.ListOrgs(ctx)
	if err != nil {
		return nil, s.orgErr("ListOrgs", err)
	}
	out := make([]*pb.Org, len(values))
	for i := range values {
		out[i] = orgProto(values[i])
	}
	return &pb.ListOrgsResponse{Orgs: out}, nil
}

// orgInForce is the org a request is served for: the one the caller's
// principal carries. A request field cannot choose another tenant.
func orgInForce(ctx context.Context) org.OrgID { return org.OrgFromContext(ctx) }

func (s *server) GetOrg(ctx context.Context, _ *pb.GetOrgRequest) (*pb.GetOrgResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	value, err := s.deps.Orgs.GetOrg(ctx, orgInForce(ctx))
	if err != nil {
		return nil, s.orgErr("GetOrg", err)
	}
	return &pb.GetOrgResponse{Org: orgProto(value)}, nil
}

func (s *server) CreateOrg(ctx context.Context, r *pb.CreateOrgRequest) (*pb.CreateOrgResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	value, err := s.deps.Orgs.CreateOrg(ctx, orgValue(&pb.Org{Id: r.GetId(), Name: r.GetName()}))
	if err != nil {
		return nil, s.orgErr("CreateOrg", err)
	}
	return &pb.CreateOrgResponse{Org: orgProto(value)}, nil
}

func (s *server) ListWorkspaces(ctx context.Context, _ *pb.ListWorkspacesRequest) (*pb.ListWorkspacesResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	values, err := s.deps.Orgs.ListWorkspaces(ctx, orgInForce(ctx))
	if err != nil {
		return nil, s.orgErr("ListWorkspaces", err)
	}
	out := make([]*pb.Workspace, len(values))
	for i := range values {
		out[i] = workspaceProto(values[i])
	}
	return &pb.ListWorkspacesResponse{Workspaces: out}, nil
}

func (s *server) CreateWorkspace(ctx context.Context, r *pb.CreateWorkspaceRequest) (*pb.CreateWorkspaceResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	value := workspaceValue(&pb.Workspace{Id: r.GetId(), OrgId: string(orgInForce(ctx)), Name: r.GetName(), Environment: r.GetEnvironment()})
	created, err := s.deps.Orgs.CreateWorkspace(ctx, value)
	if err != nil {
		return nil, s.orgErr("CreateWorkspace", err)
	}
	return &pb.CreateWorkspaceResponse{Workspace: workspaceProto(created)}, nil
}

func (s *server) ListMembers(ctx context.Context, _ *pb.ListMembersRequest) (*pb.ListMembersResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	values, err := s.deps.Orgs.ListMembers(ctx, orgInForce(ctx))
	if err != nil {
		return nil, s.orgErr("ListMembers", err)
	}
	out := make([]*pb.Member, len(values))
	for i := range values {
		out[i] = memberProto(values[i])
	}
	return &pb.ListMembersResponse{Members: out}, nil
}

func (s *server) SetMembership(ctx context.Context, r *pb.SetMembershipRequest) (*pb.SetMembershipResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	err := s.deps.Orgs.EnsureMembership(ctx, org.Membership{
		IdentityID:  identity.IdentityID(r.GetIdentityId()),
		OrgID:       orgInForce(ctx),
		WorkspaceID: org.WorkspaceID(r.GetWorkspaceId()),
		Role:        org.Role(r.GetRole()),
	})
	if err != nil {
		return nil, s.orgErr("SetMembership", err)
	}
	return &pb.SetMembershipResponse{}, nil
}

func (s *server) RemoveMembership(ctx context.Context, r *pb.RemoveMembershipRequest) (*pb.RemoveMembershipResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	err := s.deps.Orgs.RemoveMembership(ctx, org.Membership{
		IdentityID:  identity.IdentityID(r.GetIdentityId()),
		OrgID:       orgInForce(ctx),
		WorkspaceID: org.WorkspaceID(r.GetWorkspaceId()),
	})
	if err != nil {
		return nil, s.orgErr("RemoveMembership", err)
	}
	return &pb.RemoveMembershipResponse{}, nil
}

func (s *server) AssignAgent(ctx context.Context, r *pb.AssignAgentRequest) (*pb.AssignAgentResponse, error) {
	if s.deps.Orgs == nil {
		return nil, errOrgsUnavailable
	}
	err := s.deps.Orgs.AssignAgent(ctx, org.AgentAssignment{
		IdentityID: identity.IdentityID(r.GetIdentityId()),
		OrgID:      orgInForce(ctx),
	})
	if err != nil {
		return nil, s.orgErr("AssignAgent", err)
	}
	return &pb.AssignAgentResponse{}, nil
}
