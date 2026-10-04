package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// policyOrg is the org a policy request acts in: the caller's.
func policyOrg(r *http.Request) org.OrgID {
	if principal, ok := RequestPrincipal(r.Context()); ok {
		return principal.Org
	}
	return access.SharedTokenOwner().Org
}

// handleListPolicies lists the caller's org policies at every level an org
// edits. Instance policies span orgs and are not listed.
func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	if s.Policies == nil {
		http.Error(w, "policies unavailable", http.StatusServiceUnavailable)
		return
	}
	policies, err := s.Policies.ListPolicies(r.Context())
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	orgID := policyOrg(r)
	out := []access.Policy{}
	for _, p := range policies {
		if p.Level != access.LevelInstance && p.OrgID == orgID {
			out = append(out, p)
		}
	}
	writeJSON(w, map[string]any{"policies": out, "shipped": shippedPolicyIDs()})
}

func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.decodePolicy(w, r)
	if !ok {
		return
	}
	version, err := s.Policies.PutPolicy(r.Context(), p)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, map[string]int64{"version": version})
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.decodePolicy(w, r)
	if !ok {
		return
	}
	if err := s.Policies.DeletePolicy(r.Context(), p); err != nil {
		writePolicyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodePolicy reads a policy and pins it to the caller's org: no request
// field chooses the org, and instance policies are not edited here.
func (s *Server) decodePolicy(w http.ResponseWriter, r *http.Request) (access.Policy, bool) {
	if s.Policies == nil {
		http.Error(w, "policies unavailable", http.StatusServiceUnavailable)
		return access.Policy{}, false
	}
	var p access.Policy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		http.Error(w, "invalid policy: "+err.Error(), http.StatusBadRequest)
		return access.Policy{}, false
	}
	if p.Level == access.LevelInstance {
		http.Error(w, "instance policies are not edited from an org", http.StatusForbidden)
		return access.Policy{}, false
	}
	p.OrgID = policyOrg(r)
	return p, true
}

func writePolicyError(w http.ResponseWriter, err error) {
	if errors.Is(err, access.ErrPolicyLockout) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeControlPlaneError(w, err)
}

func shippedPolicyIDs() []string {
	return []string{access.PolicyOrgRead, access.PolicyOrgEdit, access.PolicyOrgAdmin, access.PolicyOrgOwner}
}
