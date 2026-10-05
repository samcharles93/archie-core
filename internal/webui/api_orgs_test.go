package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// fakeOrgs is an in-memory org.API that validates memberships the way the
// store does.
type fakeOrgs struct {
	orgs       []org.Org
	workspaces map[org.OrgID][]org.Workspace
	members    map[org.OrgID][]org.Member
	agents     []org.AgentAssignment
}

func newFakeOrgs() *fakeOrgs {
	return &fakeOrgs{
		orgs:       []org.Org{{ID: org.DefaultOrgID, Name: "System"}},
		workspaces: map[org.OrgID][]org.Workspace{},
		members: map[org.OrgID][]org.Member{
			org.DefaultOrgID: {{IdentityID: "seed", Kind: identity.KindUser, DisplayName: "Seed", Role: org.RoleDeveloper}},
		},
	}
}

func (f *fakeOrgs) ListOrgs(context.Context) ([]org.Org, error) { return f.orgs, nil }

func (f *fakeOrgs) GetOrg(_ context.Context, id org.OrgID) (org.Org, error) {
	for _, o := range f.orgs {
		if o.ID == id {
			return o, nil
		}
	}
	return org.Org{}, org.ErrOrgNotFound
}

func (f *fakeOrgs) ListWorkspaces(_ context.Context, id org.OrgID) ([]org.Workspace, error) {
	return f.workspaces[id], nil
}

func (f *fakeOrgs) CreateWorkspace(_ context.Context, value org.Workspace) (org.Workspace, error) {
	if err := value.Validate(); err != nil {
		return org.Workspace{}, err
	}
	f.workspaces[value.OrgID] = append(f.workspaces[value.OrgID], value)
	return value, nil
}

func (f *fakeOrgs) ListMembers(_ context.Context, id org.OrgID) ([]org.Member, error) {
	return f.members[id], nil
}

func (f *fakeOrgs) EnsureMembership(_ context.Context, value org.Membership) error {
	if err := value.Validate(); err != nil {
		return err
	}
	list := f.members[value.OrgID]
	for i := range list {
		if list[i].IdentityID == value.IdentityID && list[i].WorkspaceID == value.WorkspaceID {
			list[i].Role = value.Role
			return nil
		}
	}
	f.members[value.OrgID] = append(list, org.Member{IdentityID: value.IdentityID, WorkspaceID: value.WorkspaceID, Role: value.Role})
	return nil
}

func (f *fakeOrgs) RemoveMembership(_ context.Context, value org.Membership) error {
	kept := f.members[value.OrgID][:0]
	for _, m := range f.members[value.OrgID] {
		if m.IdentityID == value.IdentityID && m.WorkspaceID == value.WorkspaceID {
			continue
		}
		kept = append(kept, m)
	}
	f.members[value.OrgID] = kept
	return nil
}

func (f *fakeOrgs) AssignAgent(_ context.Context, value org.AgentAssignment) error {
	f.agents = append(f.agents, value)
	return nil
}

// orgRoutes is the org mux behind the same credential and authorize wrappers
// the real server mounts.
func orgRoutes(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/orgs", s.handleOrgsList)
	mux.HandleFunc("GET /api/orgs/{id}", s.handleOrgGet)
	mux.HandleFunc("GET /api/orgs/{id}/workspaces", s.handleOrgWorkspacesList)
	mux.HandleFunc("POST /api/orgs/{id}/workspaces", s.handleOrgWorkspaceCreate)
	mux.HandleFunc("GET /api/orgs/{id}/members", s.handleOrgMembersList)
	mux.HandleFunc("PUT /api/orgs/{id}/members/{identity}", s.handleOrgMemberSet)
	mux.HandleFunc("DELETE /api/orgs/{id}/members/{identity}", s.handleOrgMemberRemove)
	mux.HandleFunc("PUT /api/orgs/{id}/agents/{identity}", s.handleOrgAgentAssign)
	return s.requireToken(s.authorize(mux))
}

// TestOrgRoutes drives the dashboard's org API through its HTTP handlers: the
// shared-token session lists its own org, its workspaces and members, adds a
// membership, changes its role and removes it, and an unknown role is refused
// naming the role.
func TestOrgRoutes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{"lists the caller's org", http.MethodGet, "/api/orgs", "", http.StatusOK, string(org.DefaultOrgID)},
		{"another org reads as not found", http.MethodGet, "/api/orgs/other", "", http.StatusNotFound, ""},
		{"reads the caller's org", http.MethodGet, "/api/orgs/" + string(org.DefaultOrgID), "", http.StatusOK, "System"},
		{"lists workspaces", http.MethodGet, "/api/orgs/" + string(org.DefaultOrgID) + "/workspaces", "", http.StatusOK, "workspaces"},
		{"creates a workspace", http.MethodPost, "/api/orgs/" + string(org.DefaultOrgID) + "/workspaces", `{"id":"ops","name":"Ops"}`, http.StatusCreated, "ops"},
		{"lists members", http.MethodGet, "/api/orgs/" + string(org.DefaultOrgID) + "/members", "", http.StatusOK, "seed"},
		{"adds a membership", http.MethodPut, "/api/orgs/" + string(org.DefaultOrgID) + "/members/sam", `{"role":"developer"}`, http.StatusOK, "developer"},
		{"changes a membership's role", http.MethodPut, "/api/orgs/" + string(org.DefaultOrgID) + "/members/seed", `{"role":"admin"}`, http.StatusOK, "admin"},
		{"removes a membership", http.MethodDelete, "/api/orgs/" + string(org.DefaultOrgID) + "/members/seed", "", http.StatusNoContent, ""},
		{"assigns an agent", http.MethodPut, "/api/orgs/" + string(org.DefaultOrgID) + "/agents/bot", "", http.StatusOK, "true"},
		{"an unknown role is refused naming it", http.MethodPut, "/api/orgs/" + string(org.DefaultOrgID) + "/members/sam", `{"role":"superuser"}`, http.StatusBadRequest, "superuser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{Token: "shared", Access: allowAll{}, Principals: stubPrincipals(true), Orgs: newFakeOrgs()}
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, body)
			req.Header.Set("Authorization", "Bearer shared")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			orgRoutes(s).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}
