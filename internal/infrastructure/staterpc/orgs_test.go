package staterpc

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// fakeOrgs records the calls the org RPCs make, validating memberships the way
// the store does.
type fakeOrgs struct {
	workspaces []org.Workspace
	members    []org.Membership
	agents     []org.AgentAssignment
}

func (*fakeOrgs) CreateOrg(_ context.Context, value org.Org) (org.Org, error) { return value, nil }

func (*fakeOrgs) GetOrg(_ context.Context, id org.OrgID) (org.Org, error) {
	return org.Org{ID: id, Name: "Acme"}, nil
}

func (*fakeOrgs) ListOrgs(context.Context) ([]org.Org, error) { return nil, nil }

func (f *fakeOrgs) CreateWorkspace(_ context.Context, value org.Workspace) (org.Workspace, error) {
	f.workspaces = append(f.workspaces, value)
	return value, nil
}

func (*fakeOrgs) ListWorkspaces(context.Context, org.OrgID) ([]org.Workspace, error) { return nil, nil }

func (f *fakeOrgs) EnsureMembership(_ context.Context, value org.Membership) error {
	if err := value.Validate(); err != nil {
		return err
	}
	f.members = append(f.members, value)
	return nil
}

func (*fakeOrgs) ListMembers(context.Context, org.OrgID) ([]org.Member, error) { return nil, nil }

func (f *fakeOrgs) RemoveMembership(_ context.Context, value org.Membership) error {
	f.members = append(f.members, value)
	return nil
}

func (*fakeOrgs) ListOrgAgents(context.Context, org.OrgID) ([]org.AgentAssignment, error) {
	return nil, nil
}

func (f *fakeOrgs) AssignAgent(_ context.Context, value org.AgentAssignment) error {
	f.agents = append(f.agents, value)
	return nil
}

func (*fakeOrgs) OrgForIdentity(context.Context, identity.IdentityID) (org.OrgID, error) {
	return "", nil
}

// TestOrgRPCsActInTheCallersOrg pins the tenant guard: a request field cannot
// move a call into another org.
func TestOrgRPCsActInTheCallersOrg(t *testing.T) {
	repo := &fakeOrgs{}
	s := &server{deps: Deps{Orgs: repo, Log: slog.New(slog.DiscardHandler)}}
	ctx := org.WithOrg(context.Background(), "acme")

	got, err := s.GetOrg(ctx, &pb.GetOrgRequest{OrgId: "other"})
	if err != nil || got.GetOrg().GetId() != "acme" {
		t.Fatalf("GetOrg acted in %q, err %v", got.GetOrg().GetId(), err)
	}
	if _, err := s.SetMembership(ctx, &pb.SetMembershipRequest{OrgId: "other", IdentityId: "sam", Role: string(org.RoleDeveloper)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveMembership(ctx, &pb.RemoveMembershipRequest{OrgId: "other", IdentityId: "sam"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignAgent(ctx, &pb.AssignAgentRequest{OrgId: "other", IdentityId: "bot"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, &pb.CreateWorkspaceRequest{OrgId: "other", Id: "ws", Name: "Workspace"}); err != nil {
		t.Fatal(err)
	}

	for _, membership := range repo.members {
		if membership.OrgID != "acme" {
			t.Fatalf("membership acted in %q, want acme", membership.OrgID)
		}
	}
	if len(repo.agents) != 1 || repo.agents[0].OrgID != "acme" {
		t.Fatalf("agents = %#v", repo.agents)
	}
	if len(repo.workspaces) != 1 || repo.workspaces[0].OrgID != "acme" {
		t.Fatalf("workspaces = %#v", repo.workspaces)
	}
}

// TestSetMembershipRefusesUnknownRole pins that the store's role validation
// reaches the wire as InvalidArgument and names the role.
func TestSetMembershipRefusesUnknownRole(t *testing.T) {
	s := &server{deps: Deps{Orgs: &fakeOrgs{}, Log: slog.New(slog.DiscardHandler)}}
	_, err := s.SetMembership(org.WithOrg(context.Background(), "acme"), &pb.SetMembershipRequest{IdentityId: "sam", Role: "superuser"})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "superuser") {
		t.Fatalf("err = %v, want InvalidArgument naming the role", err)
	}
}
