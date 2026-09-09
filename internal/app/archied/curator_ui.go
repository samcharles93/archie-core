package archied

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/webui"
)

// curatorsUIAdapter narrows the running curator registry to the webui-owned
// CuratorStatus view. The webui deliberately does not link the curator
// pass-engine contract (archie-core-8cda.5.6), so the process that owns the
// registry supplies this adapter at bootstrap. It converts health and
// activity records to their wire views; it never hands the UI a live
// engine.
type curatorsUIAdapter struct{ r *curator.Registry }

var _ webui.CuratorStatus = curatorsUIAdapter{}

func (a curatorsUIAdapter) Names() []string { return a.r.Names() }

func (a curatorsUIAdapter) Health(ctx context.Context) map[string]webui.CuratorHealthView {
	health := a.r.Health(ctx)
	views := make(map[string]webui.CuratorHealthView, len(health))
	for name, h := range health {
		views[name] = webui.CuratorHealthView{Status: string(h.Status), Message: h.Message}
	}
	return views
}

func (a curatorsUIAdapter) Activity(name string) (webui.CuratorActivity, bool) {
	activity, ok := a.r.Activity(name)
	if !ok {
		return webui.CuratorActivity{}, false
	}
	view := webui.CuratorActivity{
		LastRunAt:      activity.LastRunAt,
		LastRunActions: activity.LastRunActions,
		Recent:         make([]webui.CuratorActionView, 0, len(activity.Recent)),
	}
	for _, a2 := range activity.Recent {
		view.Recent = append(view.Recent, webui.CuratorActionView{
			At:     a2.At,
			Type:   a2.Type,
			Detail: a2.Detail,
			Reason: a2.Reason,
		})
	}
	return view, true
}
