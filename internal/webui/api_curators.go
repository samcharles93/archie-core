package webui

import (
	"context"
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

// CuratorStatus is the dashboard's view of the curator registry.
type CuratorStatus interface {
	Names() []string
	Health(ctx context.Context) map[string]CuratorHealthView
	Activity(name string) (CuratorActivity, bool)
}

// CuratorActivity is one curator's recent run history, as the webui
// renders it.
type CuratorActivity struct {
	LastRunAt      time.Time
	LastRunActions int
	Recent         []CuratorActionView
}

// CuratorView is one registered curator's observable state: identity,
// health, and recent activity. Backed by CuratorStatus -- see
// handleCurators.
type CuratorView struct {
	Name   string            `json:"name"`
	Health CuratorHealthView `json:"health"`
	// LastRunAt is nil when the curator has never run.
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
// ran, what it changed, and why. The registry
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
				Status:  h.Status,
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
