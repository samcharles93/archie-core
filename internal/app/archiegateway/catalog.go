package archiegateway

import (
	"context"
	"fmt"
	"slices"

	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/logrpc"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/skill"
)

// setSkillList records the catalogue the last refresh registered, so the
// dashboard reads exactly what skill_activate serves.
func (b *server) setSkillList(entries []skill.CatalogEntry) {
	b.skillMu.Lock()
	defer b.skillMu.Unlock()
	b.skillList = slices.Clone(entries)
}

// chatCatalog exposes this process's runtime surfaces over ChatService: the
// live skill catalogue and the curator registry. The Gateway hosts no
// channels, so channel reload is left unset and answers Unavailable.
func (b *server) chatCatalog() gatewayrpc.Catalog {
	return gatewayrpc.Catalog{
		Logs:     b.recentLogs,
		Skills:   b.skillEntries,
		Curators: b.curatorStates,
	}
}

func (b *server) skillEntries() []gatewayrpc.SkillEntry {
	b.skillMu.Lock()
	entries := slices.Clone(b.skillList)
	b.skillMu.Unlock()
	cfg := b.cfgHolder.Get()
	out := make([]gatewayrpc.SkillEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, gatewayrpc.SkillEntry{
			Name:        e.Name,
			Description: e.Description,
			Workflow:    e.Workflow,
			Source:      skillSource(e, cfg.WorkDir, cfg.SkillsDir),
		})
	}
	return out
}

// curatorStates reads the live curator registry: names, point-in-time health
// and recent activity. A process without a registry answers empty.
func (b *server) curatorStates(ctx context.Context) []gatewayrpc.CuratorState {
	registry := b.curatorRegistry
	if registry == nil {
		return nil
	}
	names := registry.Names()
	health := registry.Health(ctx)
	out := make([]gatewayrpc.CuratorState, 0, len(names))
	for _, name := range names {
		h := health[name]
		state := gatewayrpc.CuratorState{Name: name, HealthStatus: string(h.Status), HealthMessage: h.Message}
		if activity, ok := registry.Activity(name); ok {
			state.HasActivity = true
			state.LastRunAt = activity.LastRunAt
			state.LastRunActions = activity.LastRunActions
			for _, action := range activity.Recent {
				state.Recent = append(state.Recent, gatewayrpc.CuratorAction{
					At: action.At, Type: action.Type, Detail: action.Detail, Reason: action.Reason,
				})
			}
		}
		out = append(out, state)
	}
	return out
}

// skillSource labels where a skill was loaded from. storedDir wins because a
// resource-held skill carries no root; sharedDir is checked before workDir
// because when they are equal the roots collapse to one entry.
func skillSource(entry skill.CatalogEntry, workDir, sharedDir string) string {
	switch {
	case entry.Body != "":
		return "stored skills"
	case sharedDir != "" && entry.Root == sharedDir:
		return "shared skills"
	case entry.Root == workDir:
		return "project skills"
	default:
		return "user-global skills"
	}
}

func (b *server) recentLogs(ctx context.Context, service string, q logging.Query) (logging.Result, error) {
	switch service {
	case "", presence.Gateway:
		return b.logFeed.Read(q)
	case presence.Daemon:
		return logrpc.ReadDaemon(ctx, b.taskActionsConn, q)
	default:
		return logging.Result{}, fmt.Errorf("unknown log service %q", service)
	}
}
