package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
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

func (f *fakeOrgs) CreateOrg(_ context.Context, value org.Org, owner identity.IdentityID) (org.Org, error) {
	if err := value.Validate(); err != nil {
		return org.Org{}, err
	}
	f.orgs = append(f.orgs, value)
	f.members[value.ID] = []org.Member{{IdentityID: owner, Role: org.RoleOwner}}
	return value, nil
}

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
	mux.HandleFunc("POST /api/orgs", s.handleOrgCreate)
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

// rolePrincipals gives each identity id the org-sys role of the same name.
type rolePrincipals struct{}

func (rolePrincipals) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	return access.Principal{IdentityID: id, Org: org.DefaultOrgID, Memberships: []org.Membership{
		{IdentityID: id, OrgID: org.DefaultOrgID, Role: org.Role(id)},
	}}, nil
}

// TestOrgMembershipNeedsItsRole holds membership changes to the shipped role
// policies: a developer manages no members, an admin manages members but can
// neither grant the owner role nor change an owner's, and an owner can.
func TestOrgMembershipNeedsItsRole(t *testing.T) {
	engine, err := infraaccess.New(access.ShippedOrgPolicies(org.DefaultOrgID))
	if err != nil {
		t.Fatal(err)
	}
	members := "/api/orgs/" + string(org.DefaultOrgID) + "/members/"
	tests := []struct {
		name       string
		caller     string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"a developer cannot add a member", "developer", http.MethodPut, members + "sam", `{"role":"viewer"}`, http.StatusForbidden},
		{"a developer cannot make itself owner", "developer", http.MethodPut, members + "developer", `{"role":"owner"}`, http.StatusForbidden},
		{"an admin adds a member", "admin", http.MethodPut, members + "sam", `{"role":"developer"}`, http.StatusOK},
		{"an admin cannot make itself owner", "admin", http.MethodPut, members + "admin", `{"role":"owner"}`, http.StatusForbidden},
		{"an admin cannot demote an owner", "admin", http.MethodPut, members + "boss", `{"role":"viewer"}`, http.StatusForbidden},
		{"an admin cannot remove an owner", "admin", http.MethodDelete, members + "boss", "", http.StatusForbidden},
		{"an owner grants owner", "owner", http.MethodPut, members + "sam", `{"role":"owner"}`, http.StatusOK},
		{"a developer cannot assign an agent", "developer", http.MethodPut, "/api/orgs/" + string(org.DefaultOrgID) + "/agents/bot", "", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgs := newFakeOrgs()
			orgs.members[org.DefaultOrgID] = append(orgs.members[org.DefaultOrgID], org.Member{IdentityID: "boss", Role: org.RoleOwner})
			s := &Server{
				Access: engine, Principals: rolePrincipals{}, Orgs: orgs,
				Authenticate: func(_ context.Context, token string) (identity.Identity, error) {
					return identity.Identity{ID: identity.IdentityID(token), Kind: identity.KindUser}, nil
				},
			}
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, body)
			req.Header.Set("Authorization", "Bearer "+tt.caller)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			orgRoutes(s).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestInstanceAdminOrgs holds the instance-admin boundary: an owner of org-sys
// lists and opens every org and creates one with its owner; an admin of
// org-sys sees only its own org and cannot create one.
func TestInstanceAdminOrgs(t *testing.T) {
	engine, err := infraaccess.New(access.ShippedOrgPolicies(org.DefaultOrgID))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		caller     string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{"an instance admin lists every org", "owner", http.MethodGet, "/api/orgs", "", http.StatusOK, "acme"},
		{"anyone else lists only their own", "admin", http.MethodGet, "/api/orgs", "", http.StatusOK, string(org.DefaultOrgID)},
		{"an instance admin opens another org", "owner", http.MethodGet, "/api/orgs/acme/members", "", http.StatusOK, "boss"},
		{"anyone else does not", "admin", http.MethodGet, "/api/orgs/acme/members", "", http.StatusNotFound, ""},
		{"an instance admin creates an org with its owner", "owner", http.MethodPost, "/api/orgs", `{"id":"beta","name":"Beta","owner_identity":"sam"}`, http.StatusCreated, "beta"},
		{"anyone else cannot", "admin", http.MethodPost, "/api/orgs", `{"id":"beta","name":"Beta","owner_identity":"sam"}`, http.StatusForbidden, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgs := newFakeOrgs()
			orgs.orgs = append(orgs.orgs, org.Org{ID: "acme", Name: "Acme"})
			orgs.members["acme"] = []org.Member{{IdentityID: "boss", Role: org.RoleOwner}}
			s := &Server{
				Access: engine, Principals: rolePrincipals{}, Orgs: orgs,
				Authenticate: func(_ context.Context, token string) (identity.Identity, error) {
					return identity.Identity{ID: identity.IdentityID(token), Kind: identity.KindUser}, nil
				},
			}
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, body)
			req.Header.Set("Authorization", "Bearer "+tt.caller)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			orgRoutes(s).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want %q", rec.Body.String(), tt.wantBody)
			}
			if tt.caller == "admin" && tt.path == "/api/orgs" && strings.Contains(rec.Body.String(), "acme") {
				t.Fatalf("a non-admin saw another org: %s", rec.Body.String())
			}
		})
	}
}
