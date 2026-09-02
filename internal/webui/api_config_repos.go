package webui

import (
	"encoding/json"
	"errors"
	"net/http"
)

// RepoEditableFields are the per-repository fields the dashboard may change
// through handleConfigRepoUpdate. Deliberately narrow: these three are
// booleans/small ints with no cross-field validation, unlike Gate or
// Protect, which stay file-edited (archie-core-b6ew.4/.6).
var RepoEditableFields = map[string]bool{
	"allow_concurrent": true,
	"max_retries":      true,
	"review_enabled":   true,
}

// handleConfigRepoUpdate applies one field change to one repository,
// identified by owner/name in the path. It exists because the repository
// list is a []config.Repo, not a flat dotted key -- PATCH /api/config's
// generic path (overlay.Nest + configuration.ApplyOverlayValues) has no way
// to address "element N of this slice." UpdateRepoField's implementation
// (wired by the composition root) reads the daemon's own live config.Repo
// values (never the trimmed, wire-safe RepoView), changes just the one
// field, and republishes the full repository list through the same
// validate-persist-publish path PATCH /api/config already uses -- see
// bootstrap.go's installUpdateConfigHandler for why that matters: the wire
// view omits fields (Preflight, TestGlob) that a naive round-trip through
// ConfigView would silently drop.
func (s *Server) handleConfigRepoUpdate(w http.ResponseWriter, r *http.Request) {
	if s.UpdateRepoField == nil {
		http.Error(w, ErrConfigUpdateUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	var body struct {
		Field string `json:"field"`
		Value any    `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !RepoEditableFields[body.Field] {
		http.Error(w, "field "+body.Field+" is not editable from the dashboard", http.StatusBadRequest)
		return
	}
	if err := s.UpdateRepoField(r.Context(), owner, name, body.Field, body.Value); err != nil {
		switch {
		case errors.Is(err, ErrConfigUpdateUnavailable):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		case errors.Is(err, ErrConfigUpdateInvalid):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, "repo update failed: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
