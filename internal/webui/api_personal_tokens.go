package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// Personal API tokens belong to the signed-in person; the State Store acts
// on the principal the request carries, so these handlers name no one.

func (s *Server) handlePersonalTokensList(w http.ResponseWriter, r *http.Request) {
	if !personalTokenOwner(r) {
		writeJSON(w, map[string]any{"tokens": []identity.PersonalToken{}, "available": false})
		return
	}
	if !s.personalTokensReady(w, r) {
		return
	}
	tokens, err := s.PersonalTokens.ListPersonalTokens(r.Context())
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	if tokens == nil {
		tokens = []identity.PersonalToken{}
	}
	writeJSON(w, map[string]any{"tokens": tokens, "available": true})
}

// handlePersonalTokenCreate makes a token here and sends only its hash to the
// State Store; the response is the one time the token is shown.
func (s *Server) handlePersonalTokenCreate(w http.ResponseWriter, r *http.Request) {
	if !s.personalTokensReady(w, r) {
		return
	}
	raw, subject, err := identity.NewPersonalToken()
	if err != nil {
		http.Error(w, "cannot create token", http.StatusInternalServerError)
		return
	}
	if err := s.PersonalTokens.AddPersonalToken(r.Context(), subject); err != nil {
		writeIdentityError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": subject.Subject, "token": raw})
}

func (s *Server) handlePersonalTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.personalTokensReady(w, r) {
		return
	}
	if err := s.PersonalTokens.RevokePersonalToken(r.Context(), r.PathValue("id")); err != nil {
		writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func personalTokenOwner(r *http.Request) bool {
	p, ok := access.PrincipalFromContext(r.Context())
	return ok && p.IdentityID != "" && p.IdentityID != identity.SystemID
}

func (s *Server) personalTokensReady(w http.ResponseWriter, r *http.Request) bool {
	if !personalTokenOwner(r) {
		http.Error(w, "Personal tokens need a signed-in person.", http.StatusForbidden)
		return false
	}

	if s.PersonalTokens == nil {
		http.Error(w, "personal tokens unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}
