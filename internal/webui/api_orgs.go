package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// orgReady answers Unavailable when this process hosts no org store.
func (s *Server) orgReady(w http.ResponseWriter) bool {
	if s.Orgs == nil {
		http.Error(w, "orgs unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// orgInPath resolves the route's {id}: the caller's own org, or any org for
// an instance admin. Another tenant reads as not found rather than leaking.
func (s *Server) orgInPath(w http.ResponseWriter, r *http.Request) (org.OrgID, bool) {
	id := org.OrgID(r.PathValue("id"))
	if id != policyOrg(r) && !instanceOwner(r) {
		http.Error(w, "org not found", http.StatusNotFound)
		return "", false
	}
	return id, true
}

// handleOrgsList lists every org to an instance admin and the caller's own
// to anyone else.
func (s *Server) handleOrgsList(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	values, err := s.Orgs.ListOrgs(r.Context())
	if err != nil {
		writeOrgError(w, err)
		return
	}
	caller := policyOrg(r)
	out := []org.Org{}
	for _, value := range values {
		if value.ID == caller || instanceOwner(r) {
			out = append(out, value)
		}
	}
	writeJSON(w, map[string]any{"orgs": out})
}

// handleOrgCreate creates an org with its default workspace and first owner.
// Only an instance admin may.
func (s *Server) handleOrgCreate(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	if !instanceOwner(r) {
		http.Error(w, "only an instance admin creates orgs", http.StatusForbidden)
		return
	}
	creator, ok := s.Orgs.(org.Creator)
	if !ok {
		http.Error(w, "org creation unavailable", http.StatusServiceUnavailable)
		return
	}
	var request struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		OwnerIdentity string `json:"owner_identity"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid org: "+err.Error(), http.StatusBadRequest)
		return
	}
	created, err := creator.CreateOrg(r.Context(), org.Org{ID: org.OrgID(request.ID), Name: request.Name}, identity.IdentityID(request.OwnerIdentity))
	if err != nil {
		writeOrgError(w, err)
		return
	}
	// The new org's role policies were seeded; load them so its owner can act.
	if s.ReloadAccess != nil {
		if err := s.ReloadAccess(r.Context()); err != nil {
			s.logf("reload access after org create", "err", err)
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"org": created})
}

func (s *Server) handleOrgGet(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	value, err := s.Orgs.GetOrg(r.Context(), id)
	if err != nil {
		writeOrgError(w, err)
		return
	}
	writeJSON(w, map[string]any{"org": value})
}

func (s *Server) handleOrgWorkspacesList(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	values, err := s.Orgs.ListWorkspaces(r.Context(), id)
	if err != nil {
		writeOrgError(w, err)
		return
	}
	if values == nil {
		values = []org.Workspace{}
	}
	writeJSON(w, map[string]any{"workspaces": values})
}

func (s *Server) handleOrgWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	var request struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Environment string `json:"environment"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid workspace: "+err.Error(), http.StatusBadRequest)
		return
	}
	value, err := s.Orgs.CreateWorkspace(r.Context(), org.Workspace{
		ID: org.WorkspaceID(request.ID), OrgID: id, Name: request.Name, Environment: request.Environment,
	})
	if err != nil {
		writeOrgError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"workspace": value})
}

func (s *Server) handleOrgMembersList(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	values, err := s.Orgs.ListMembers(r.Context(), id)
	if err != nil {
		writeOrgError(w, err)
		return
	}
	if values == nil {
		values = []org.Member{}
	}
	writeJSON(w, map[string]any{"members": values})
}

// handleOrgMemberSet grants or moves one identity's role in the caller's org,
// or in one workspace when the body names one.
func (s *Server) handleOrgMemberSet(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	var request struct {
		Role      string `json:"role"`
		Workspace string `json:"workspace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid membership: "+err.Error(), http.StatusBadRequest)
		return
	}
	membership := org.Membership{
		IdentityID:  identity.IdentityID(r.PathValue("identity")),
		OrgID:       id,
		WorkspaceID: org.WorkspaceID(request.Workspace),
		Role:        org.Role(request.Role),
	}
	if !s.mayChangeMembership(w, r, membership, membership.Role) {
		return
	}
	if err := s.Orgs.EnsureMembership(r.Context(), membership); err != nil {
		writeOrgError(w, err)
		return
	}
	writeJSON(w, map[string]any{"membership": membership})
}

func (s *Server) handleOrgMemberRemove(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	membership := org.Membership{
		IdentityID:  identity.IdentityID(r.PathValue("identity")),
		OrgID:       id,
		WorkspaceID: org.WorkspaceID(r.URL.Query().Get("workspace")),
	}
	if !s.mayChangeMembership(w, r, membership, "") {
		return
	}
	if err := s.Orgs.RemoveMembership(r.Context(), membership); err != nil {
		writeOrgError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOrgAgentAssign(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	err := s.Orgs.AssignAgent(r.Context(), org.AgentAssignment{
		IdentityID: identity.IdentityID(r.PathValue("identity")), OrgID: id,
	})
	if err != nil {
		writeOrgError(w, err)
		return
	}
	writeJSON(w, map[string]any{"assigned": true})
}

// writeOrgError maps the org domain's errors, and their gRPC statuses, onto
// HTTP. The remote-client path keeps the status code through unmapError's
// wrapping, so writeControlPlaneError names the same code the store sent.
// mayChangeMembership refuses, having written the error, a change that grants
// the owner role or alters an owner's membership unless the caller is an owner
// of the org: member management is an admin's grant, and without this an
// admin could make themselves owner -- and an owner of org-sys owns the
// instance.
func (s *Server) mayChangeMembership(w http.ResponseWriter, r *http.Request, target org.Membership, grant org.Role) bool {
	principal, ok := access.PrincipalFromContext(r.Context())
	if !ok {
		principal = access.SharedTokenOwner()
	}
	if principal.Role("") == org.RoleOwner {
		return true
	}
	touchesOwner := grant == org.RoleOwner
	if !touchesOwner {
		members, err := s.Orgs.ListMembers(r.Context(), target.OrgID)
		if err != nil {
			writeOrgError(w, err)
			return false
		}
		for _, m := range members {
			if m.IdentityID == target.IdentityID && m.WorkspaceID == target.WorkspaceID && m.Role == org.RoleOwner {
				touchesOwner = true
			}
		}
	}
	if touchesOwner {
		http.Error(w, "only an owner may grant or change the owner role", http.StatusForbidden)
		return false
	}
	return true
}

func writeOrgError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, org.ErrInvalidRole), errors.Is(err, org.ErrInvalidOrg),
		errors.Is(err, org.ErrInvalidMembership), errors.Is(err, org.ErrInvalidWorkspace),
		errors.Is(err, org.ErrInvalidAssignment):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, org.ErrOrgNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, org.ErrOrgExists), errors.Is(err, org.ErrWorkspaceExists), errors.Is(err, org.ErrMembershipExists):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		writeControlPlaneError(w, err)
	}
}
