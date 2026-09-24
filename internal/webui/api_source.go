package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// Capture sources and their signing setting (docs/prds/event-automation.md
// "Sources"). A secret is returned only by the two writes that generate it,
// so the operator can copy it into the sender; every other read withholds it.
// Turning signing off is a request plus an approval, both behind the same
// mutation gate that arms a binding.

func (s *Server) handleSourcesList(w http.ResponseWriter, r *http.Request) {
	if s.Sources == nil {
		http.Error(w, "sources not configured", http.StatusServiceUnavailable)
		return
	}
	list, err := s.Sources.ListSources(r.Context())
	if err != nil {
		s.Log.Error("list sources", "err", err)
		http.Error(w, "list sources failed", http.StatusInternalServerError)
		return
	}
	for i := range list {
		list[i].Secret = ""
	}
	writeJSON(w, map[string]any{"sources": list})
}

func (s *Server) handleSourceCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if s.Sources == nil {
		http.Error(w, "sources not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	src, err := source.New(req.Path)
	if errors.Is(err, source.ErrInvalidPath) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err == nil {
		err = s.Sources.InsertSource(r.Context(), src)
	}
	if err != nil {
		s.sourceError(w, "create source", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, src)
}

// handleSourceSigning turns signing on at once, or requests turning it off.
func (s *Server) handleSourceSigning(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Signed bool `json:"signed"`
	}
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	transition := (*source.Source).RequestUnsigned
	if req.Signed {
		transition = (*source.Source).RequireSigning
	}
	s.transitionSource(w, r, transition)
}

func (s *Server) handleSourceApproveUnsigned(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	s.transitionSource(w, r, (*source.Source).ApproveUnsigned)
}

// transitionSource applies one domain signing transition and writes it with
// the state it was decided from, so a concurrent change is refused.
func (s *Server) transitionSource(w http.ResponseWriter, r *http.Request, transition func(*source.Source) error) {
	src, ok := s.loadSource(w, r)
	if !ok {
		return
	}
	from := src.Signing
	if err := transition(src); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := s.Sources.SetSourceSigning(r.Context(), src.Path, from, src.Signing); err != nil {
		s.sourceError(w, "set source signing", err)
		return
	}
	src.Secret = ""
	writeJSON(w, src)
}

// handleSourceSecret generates a new secret and returns it once.
func (s *Server) handleSourceSecret(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	src, ok := s.loadSource(w, r)
	if !ok {
		return
	}
	secret, err := source.NewSecret()
	if err == nil {
		err = s.Sources.SetSourceSecret(r.Context(), src.Path, secret)
	}
	if err != nil {
		s.sourceError(w, "set source secret", err)
		return
	}
	src.Secret = secret
	writeJSON(w, src)
}

func (s *Server) loadSource(w http.ResponseWriter, r *http.Request) (*source.Source, bool) {
	if s.Sources == nil {
		http.Error(w, "sources not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	src, err := s.Sources.GetSource(r.Context(), r.PathValue("path"))
	if err != nil {
		s.sourceError(w, "get source", err)
		return nil, false
	}
	if src == nil {
		http.Error(w, "source not found", http.StatusNotFound)
		return nil, false
	}
	return src, true
}

func (s *Server) sourceError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, storecontract.ErrSourcePathTaken):
		http.Error(w, "source path already taken", http.StatusConflict)
	case errors.Is(err, storecontract.ErrSourceSigningStale):
		http.Error(w, "source signing changed; reload and retry", http.StatusConflict)
	case errors.Is(err, storecontract.ErrSourceNotFound):
		http.Error(w, "source not found", http.StatusNotFound)
	default:
		s.Log.Error(what, "err", err)
		http.Error(w, what+" failed", http.StatusInternalServerError)
	}
}

// unsignedSources is the set of source paths approved to take unsigned
// events. With no source store nothing is unsigned.
func (s *Server) unsignedSources(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	if s.Sources == nil {
		return out, nil
	}
	list, err := s.Sources.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, src := range list {
		if src.Unsigned() {
			out[src.Path] = true
		}
	}
	return out, nil
}
