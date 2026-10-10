package webui

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
// to anyone else, naming the caller's org and whether it may switch.
func (s *Server) handleOrgsList(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	values, err := s.Orgs.ListOrgs(r.Context())
	if err != nil {
		writeOrgError(w, err)
		return
	}
	caller, admin := policyOrg(r), instanceOwner(r)
	out := []org.Org{}
	for _, value := range values {
		if value.ID == caller || admin {
			out = append(out, value)
		}
	}
	writeJSON(w, map[string]any{"orgs": out, "current": caller, "instance_admin": admin})
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

// handleOrgMemberAdd adds a person by the subject their provider asserts or by
// their email, creating the identity and binding it so that person's next
// sign-in resolves to it with this role. An already-bound subject keeps its
// identity and only gains the membership.
func (s *Server) handleOrgMemberAdd(w http.ResponseWriter, r *http.Request) {
	if !s.orgReady(w) {
		return
	}
	id, ok := s.orgInPath(w, r)
	if !ok {
		return
	}
	if s.Subjects == nil || s.Identities == nil || s.Issuer == "" {
		http.Error(w, "adding a person needs a configured identity provider", http.StatusServiceUnavailable)
		return
	}
	request, bound, name, ok := s.decodeMemberAdd(w, r)
	if !ok {
		return
	}
	existing, err := s.Subjects.ResolveSubject(r.Context(), bound)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		writeIdentityError(w, err)
		return
	}
	unbound := err != nil
	membership := org.Membership{
		IdentityID: existing.ID, OrgID: id,
		WorkspaceID: org.WorkspaceID(request.Workspace), Role: org.Role(request.Role),
	}
	if unbound {
		if membership.IdentityID, err = randomIdentityID(); err != nil {
			http.Error(w, "cannot create identity ID", http.StatusInternalServerError)
			return
		}
	}
	if !s.mayChangeMembership(w, r, membership, membership.Role) {
		return
	}
	if unbound && !s.createBoundIdentity(w, r, membership.IdentityID, name, bound) {
		return
	}
	if err := s.Orgs.EnsureMembership(r.Context(), membership); err != nil {
		writeOrgError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"membership": membership})
}

type memberAddRequest struct {
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Workspace   string `json:"workspace"`
}

// decodeMemberAdd reads and validates an add-member body, returning the
// provider subject it names and the display name to give a new identity. It
// has written the error when ok is false.
func (s *Server) decodeMemberAdd(w http.ResponseWriter, r *http.Request) (request memberAddRequest, bound identity.Subject, name string, ok bool) {
	request, ok = decodeBody[memberAddRequest](w, r)
	if !ok {
		return
	}
	name = strings.TrimSpace(request.DisplayName)
	subject, email := strings.TrimSpace(request.Subject), strings.TrimSpace(request.Email)
	switch {
	case (subject == "") == (email == ""):
		http.Error(w, "give either a subject or an email", http.StatusBadRequest)
		return request, bound, name, false
	case subject != "":
		bound, name = identity.Subject{Issuer: s.Issuer, Subject: subject}, cmp.Or(name, subject)
	default:
		bound, name = identity.EmailSubject(s.Issuer, email), cmp.Or(name, email)
	}
	if err := org.Role(request.Role).Validate(); err != nil {
		writeOrgError(w, err)
		return request, bound, name, false
	}
	return request, bound, name, true
}

// createBoundIdentity makes a user identity and binds subject to it, having
// written the error and returned false when either step fails.
func (s *Server) createBoundIdentity(w http.ResponseWriter, r *http.Request, id identity.IdentityID, name string, subject identity.Subject) bool {
	audit, err := webAudit(r.Context())
	if err != nil {
		http.Error(w, "cannot create request ID", http.StatusInternalServerError)
		return false
	}
	value, err := identity.New(id, identity.KindUser, name)
	if err == nil {
		_, err = s.Identities.Create(r.Context(), value, audit)
	}
	if err == nil {
		err = s.Subjects.BindSubject(r.Context(), id, subject, audit)
	}
	if err != nil {
		writeIdentityError(w, err)
		return false
	}
	return true
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
