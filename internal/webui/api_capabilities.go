package webui

import "net/http"

// handleCapabilities reports which dashboard sections this process can
// actually serve.
//
// The dashboard is served by the extracted UI process, which holds two remote
// contracts (Gateway ChatContract and the State Store) and nothing else.
// Sections whose capability lives only in the daemon still answer -- with an
// empty list, a "disabled" marker, or a 501 -- and the page cannot tell that
// apart from a deployment where nothing has happened yet. Rather than have
// every page guess, the server says which sections it can back, and the
// browser hides the rest.
//
// A section is reported available when the handle behind it is wired, which
// is the same condition its handler already uses to decide between real data
// and its degraded answer. Sections backed by the task store are absent from
// this list: the store is mandatory.
func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"sections": map[string]bool{
			"chat":     s.Chat != nil && s.Chat.Contract != nil,
			"logs":     s.LogFeed != nil,
			"skills":   s.Cfg != nil,
			"memory":   s.Memory != nil,
			"curators": s.Curators != nil,
			"channels": s.Channels != nil || s.Cfg != nil,
			"captures": s.Captures != nil,
			"mappings": s.Mappings != nil,
			"bindings": s.Bindings != nil,
			// The workflows page draws its statistics from the task store
			// and its definition list from the local registry, so it is
			// worth showing with only the former.
			"workflows": true,
			// Configuration renders from whichever source is wired; the
			// page reports its own editability (see ConfigView.Editable).
			"settings": true,
		},
	})
}
