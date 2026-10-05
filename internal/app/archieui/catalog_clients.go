package archieui

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/webui"
)

// catalogTimeout bounds the interface calls that carry no request context, so a
// hung Gateway cannot pin a dashboard request.
const catalogTimeout = 5 * time.Second

// skillCatalog serves the Skills page from the Gateway's live catalogue. A
// failed read is an empty page, never a failed dashboard.
type skillCatalog struct{ client gatewayrpc.CatalogClient }

var _ webui.SkillCatalog = skillCatalog{}

func (a skillCatalog) Skills() []webui.SkillView {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	entries, err := a.client.ListSkills(ctx)
	if err != nil {
		return nil
	}
	views := make([]webui.SkillView, 0, len(entries))
	for _, e := range entries {
		views = append(views, webui.SkillView{
			Name:        e.Name,
			Description: e.Description,
			Workflow:    e.Workflow,
			Source:      e.Source,
		})
	}
	return views
}

// curatorStatus serves the Curators page from the Gateway's curator registry.
type curatorStatus struct{ client gatewayrpc.CatalogClient }

var _ webui.CuratorStatus = curatorStatus{}

func (a curatorStatus) Names() []string {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	states, err := a.client.CuratorHealth(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(states))
	for _, state := range states {
		names = append(names, state.Name)
	}
	return names
}

func (a curatorStatus) Health(ctx context.Context) map[string]webui.CuratorHealthView {
	states, err := a.client.CuratorHealth(ctx)
	if err != nil {
		return nil
	}
	out := make(map[string]webui.CuratorHealthView, len(states))
	for _, state := range states {
		out[state.Name] = webui.CuratorHealthView{Status: state.HealthStatus, Message: state.HealthMessage}
	}
	return out
}

func (a curatorStatus) Activity(name string) (webui.CuratorActivity, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	states, err := a.client.CuratorHealth(ctx)
	if err != nil {
		return webui.CuratorActivity{}, false
	}
	for _, state := range states {
		if state.Name != name || !state.HasActivity {
			continue
		}
		activity := webui.CuratorActivity{
			LastRunAt: state.LastRunAt, LastRunActions: state.LastRunActions,
			Recent: make([]webui.CuratorActionView, 0, len(state.Recent)),
		}
		for _, action := range state.Recent {
			activity.Recent = append(activity.Recent, webui.CuratorActionView{
				At: action.At, Type: action.Type, Detail: action.Detail, Reason: action.Reason,
			})
		}
		return activity, true
	}
	return webui.CuratorActivity{}, false
}
