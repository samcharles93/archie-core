package webui

import "net/http"

// handleCapabilities reports which dashboard sections this process can serve,
// so the browser hides the rest.
func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"sections": map[string]bool{
			"chat":     s.Chat != nil && s.Chat.Contract != nil,
			"logs":     s.LogFeed != nil,
			"skills":   s.Skills != nil,
			"curators": s.Curators != nil,
			"channels": s.Channels != nil,
			"captures": s.Captures != nil,
			"mappings": s.Mappings != nil,
			"bindings": s.Bindings != nil,
			// The workflows page draws its statistics from the task store
			// and its definition list from the local registry, so it is
			// worth showing with only the former.
			"workflows": true,
			// Configuration renders from whichever source is wired.
			"settings": true,
		},
	})
}
