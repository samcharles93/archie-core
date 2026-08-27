package webui

import (
	"net/http"
	"time"
)

// CuratorActionView is one recorded curator action, as shown on the
// dashboard's curator activity view: what changed and why.
type CuratorActionView struct {
	At     time.Time `json:"at"`
	Type   string    `json:"type"`
	Detail string    `json:"detail"`
	Reason string    `json:"reason"`
}

// CuratorView is one registered curator's observable state: identity,
// health, and recent activity. Backed by curator.Registry -- see
// handleCurators.
type CuratorView struct {
	Name   string            `json:"name"`
	Health CuratorHealthView `json:"health"`
	// LastRunAt is a pointer so a curator that has never run omits the
	// field entirely instead of serializing time.Time's zero value
	// ("0001-01-01T00:00:00Z") -- json's "omitempty" does not treat a
	// zero-valued struct as empty, and a non-empty string is truthy to
	// the dashboard's "has this run yet" check.
	LastRunAt      *time.Time          `json:"last_run_at,omitempty"`
	LastRunActions int                 `json:"last_run_actions"`
	RecentActions  []CuratorActionView `json:"recent_actions"`
}

// CuratorHealthView mirrors curator.Health for the wire.
type CuratorHealthView struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// handleCurators reports which curators are registered, their point-in-time
// health, and their recent activity -- which curators exist, when each last
// ran, what it changed, and why (archie-core-1786637489932-6). The registry
// is the sole source of truth; this handler is transport only.
func (s *Server) handleCurators(w http.ResponseWriter, r *http.Request) {
	if s.Curators == nil {
		writeJSON(w, map[string]any{"curators": []CuratorView{}})
		return
	}

	names := s.Curators.Names()
	health := s.Curators.Health(r.Context())

	views := make([]CuratorView, 0, len(names))
	for _, name := range names {
		h := health[name]
		view := CuratorView{
			Name: name,
			Health: CuratorHealthView{
				Status:  string(h.Status),
				Message: h.Message,
			},
			RecentActions: []CuratorActionView{},
		}
		if activity, ok := s.Curators.Activity(name); ok && !activity.LastRunAt.IsZero() {
			at := activity.LastRunAt
			view.LastRunAt = &at
			view.LastRunActions = activity.LastRunActions
			view.RecentActions = make([]CuratorActionView, 0, len(activity.Recent))
			for _, a := range activity.Recent {
				view.RecentActions = append(view.RecentActions, CuratorActionView{
					At:     a.At,
					Type:   a.Type,
					Detail: a.Detail,
					Reason: a.Reason,
				})
			}
		}
		views = append(views, view)
	}

	writeJSON(w, map[string]any{"curators": views})
}
