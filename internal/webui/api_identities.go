package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

type identityRequest struct {
	DisplayName     string        `json:"display_name"`
	Kind            identity.Kind `json:"kind"`
	ExpectedVersion int64         `json:"expected_version"`
}

func (s *Server) handleIdentitiesList(w http.ResponseWriter, r *http.Request) {
	if s.Identities == nil {
		http.Error(w, "identity store unavailable", http.StatusServiceUnavailable)
		return
	}
	values, err := s.Identities.List(r.Context())
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	if values == nil {
		values = []identity.Identity{}
	}
	writeJSON(w, map[string]any{"identities": values})
}

func (s *Server) handleIdentityCreate(w http.ResponseWriter, r *http.Request) {
	var request identityRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	id, err := randomIdentityID()
	if err != nil {
		http.Error(w, "cannot create identity ID", http.StatusInternalServerError)
		return
	}
	audit, err := webAudit()
	if err != nil {
		http.Error(w, "cannot create request ID", http.StatusInternalServerError)
		return
	}
	value, err := identity.New(id, request.Kind, request.DisplayName)
	if err == nil {
		value, err = s.Identities.Create(r.Context(), value, audit)
	}
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, value)
}

func (s *Server) handleIdentityCommand(w http.ResponseWriter, r *http.Request) {
	var request identityRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	audit, err := webAudit()
	if err != nil {
		http.Error(w, "cannot create request ID", http.StatusInternalServerError)
		return
	}
	command := identity.Command{Type: identity.CommandType(r.PathValue("command")), DisplayName: request.DisplayName}
	value, err := s.Identities.Apply(r.Context(), identity.IdentityID(r.PathValue("id")), request.ExpectedVersion, command, audit)
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	writeJSON(w, value)
}

// webAudit attributes a dashboard write. The dashboard authenticates with one
// shared token and carries no per-user session, so the actor is the System
// identity: a real identity the repository resolves, never a name the request
// could claim for itself.
func webAudit() (identity.Audit, error) {
	id, err := newControlPlaneRequestID()
	if err != nil {
		return identity.Audit{}, err
	}
	return identity.Audit{ActorID: identity.SystemID, Source: "archie-ui", RequestID: id}, nil
}

func randomIdentityID() (identity.IdentityID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return identity.IdentityID(encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]), nil
}

func writeIdentityError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, identity.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, identity.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, identity.ErrInvalid), errors.Is(err, identity.ErrIllegalTransition), errors.Is(err, identity.ErrSystemImmutable):
		code = http.StatusBadRequest
	}
	http.Error(w, strings.TrimSpace(err.Error()), code)
}
