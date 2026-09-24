package archied

import (
	"context"
	"log/slog"
	"os"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// ReloadController re-runs the configuration load on demand and
// publishes the result through apply. A failed reload never publishes:
// apply is called only with a Document that passed validation, and apply
// itself reports failure rather than publishing a half-built config. That
// "Set is not called on the error path" property is the safety core of the
// reload feature -- a bad file, or a database layer that cannot be read,
// must leave the daemon running on the previous config.
type ReloadController struct {
	loader  *configuration.Loader
	base    string
	overlay string

	// apply receives a freshly loaded, validated Document. It is the
	// composition root's job to wire this to whatever owns the running
	// config (the daemon's Holder, the dashboard's provenance). Must be
	// non-nil; newReloadController requires it. An error from apply fails
	// the reload and leaves the running config alone.
	apply func(context.Context, *configuration.Document) error

	status atomic.Pointer[config.ReloadStatus]
}

func newReloadController(loader *configuration.Loader, base, overlay string, apply func(context.Context, *configuration.Document) error) *ReloadController {
	c := &ReloadController{loader: loader, base: base, overlay: overlay, apply: apply}
	c.status.Store(&config.ReloadStatus{})
	return c
}

// Reload re-runs the configuration load. On validation failure it
// records the error in Status and returns it; apply is not called and
// the running configuration is unchanged. An error from apply is
// recorded the same way. On success it records the reload time.
func (c *ReloadController) Reload(ctx context.Context) error {
	doc, err := c.loader.Resolve(c.base, c.overlay)
	if err == nil {
		err = c.apply(ctx, doc)
	}
	if err != nil {
		c.status.Store(&config.ReloadStatus{
			LastError:   err.Error(),
			LastErrorAt: time.Now().UTC().Format(time.RFC3339),
		})
		return err
	}
	c.status.Store(&config.ReloadStatus{
		LastReloadAt: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

// Status returns the most recent reload outcome.
func (c *ReloadController) Status() config.ReloadStatus {
	if p := c.status.Load(); p != nil {
		return *p
	}
	return config.ReloadStatus{}
}

// reloadLoop waits for a reload signal on ch and re-loads the config
// each time, until ctx is cancelled. A failed reload is logged and the
// running config is left untouched (see ReloadController.Reload).
func reloadLoop(ctx context.Context, ch <-chan os.Signal, c *ReloadController, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if err := c.Reload(ctx); err != nil {
				log.Error("config reload failed; keeping running config", "err", err)
			}
		}
	}
}

// reloadableFields are the Config fields a reload takes effect on
// without a restart. The criterion: a field is reloadable when EVERY
// consumer re-reads it. That question read as "what does the daemon
// re-read" while the daemon owned the only Holder; it stopped being
// sufficient when webui began sharing that Holder, because webui
// handlers re-read on every request. MaxRetries is the worked example:
// its only consumer is api_tasks.go:380,383, reading per-request
// s.Cfg.Get(), and nothing in the daemon reads it -- so it reloads.
// Chat.Telegram.TokenEnv is the counter-example: webui reads it per
// request AND a telegram bot built at startup consumes it, so it is
// NOT reloadable, and treating "webui reads it" as sufficient would
// display a new token while the bot uses the old one.
//
// This is deliberately an allowlist: a field added to Config later
// defaults to requires-restart, which is the safe direction and forces
// whoever adds it to decide deliberately.
var reloadableFields = map[string]bool{
	// Tickers reset on change (daemon.go Run / runIdentities).
	"PollInterval": true,
	// Poll loops re-read per cycle (daemon.go poll / pollForIdentity).
	"Repos": true,
	// Per-task snapshot: ForTask carries these into every dispatched
	// TaskContext (config.go ForTask), built fresh from d.Cfg.Get() at
	// dispatch. Pinned by TestReloadableFieldsCoverForTaskSnapshot so a
	// field added to ForTask cannot silently drift out of this list.
	"DiffCapLines": true,
	"Budgets":      true,
	"Dispatch":     true,
	"Models":       true,
	"ModelLimits":  true,
	"Notify":       true,
	// BotUser/BotEmail used to be poll-path only; ForTask now carries both
	// into the task snapshot as well, because the sandboxed worker builds
	// its own worktree.Manager and signs its own commits (agentworker
	// task_execution.go, config.go ForTask). The commit identity is
	// therefore per-dispatch and does reload. The startup-built daemon
	// trees (bootstrap.go:654) still capture both, but only to record
	// user.name/user.email in the clone's own .git/config
	// (worktree.go setIdentity) -- cosmetic, not the signature go-git
	// commits with -- so a reload applies where it counts and this stays
	// reloadable rather than requires-restart.
	"BotEmail": true,
	// cfg.Label additionally feeds IssuesWithLabel per poll cycle
	// (daemon.go:359,390) and cfg.BotUser feeds AssignedIssues (:364,377).
	// Label is not carried by ForTask, so it is pinned by hand in
	// TestReloadableFieldsCoverForTaskSnapshot.
	"Label":   true,
	"BotUser": true,
	// Worked example of the every-consumer criterion above: the sole
	// consumer is the webui maxRetriesFor handler (api_tasks.go:380,383),
	// which re-reads per request; the daemon never reads MaxRetries. The
	// per-task half of this list is pinned mechanically; this webui-only
	// entry is pinned by TestChangedNonReloadableFields.
	"MaxRetries": true,
	// Tools.Policy (MaxResultChars/SpillDir) is carried into TaskConfig by
	// ForTask (config.go) and applied fresh per dispatch via
	// agentworker.applyToolLimits. See reloadableSubFields["Tools"].
	"ToolPolicy": true,
}

// reloadableSubFields are sub-fields of structs that are otherwise
// startup-built. VolumeTTL is re-read per cycle in cleanupExpiredStorage;
// everything else in Containers (Image, MaxConcurrency,
// MaxUptime, PullPolicy, Network) is frozen in the startup-built
// container pool -- the dispatchers re-read MaxConcurrency but the pool
// captures it at construction (container/pool.go:94,163), so a change
// only partially applies and must warn requires-restart. Forge.Host is
// carried into TaskContext by ForTask (display/link building only); the
// forge client itself is startup-built, so Type/Token/TokenEnv stay
// requires-restart.
var reloadableSubFields = map[string]map[string]bool{
	"Containers": {"VolumeTTL": true},
	"Forge":      {"Host": true},
	// Policy is carried into TaskConfig by ForTask (config.go); MCPServers,
	// WebFetch and Minimax are not and stay requires-restart.
	"Tools": {"Policy": true},
}

// changedNonReloadableFields compares old and new and returns the
// dotted names of top-level Config fields whose values differ and are
// not re-read per cycle. The reload log warns about these so the
// operator knows their edit did not take effect until a restart --
// the alternative is an operator who believes a reloaded poll interval
// changed the forge token, and burns an hour discovering it did not.
func changedNonReloadableFields(old, next config.Config) []string {
	var changed []string
	ov, nv := reflect.ValueOf(old), reflect.ValueOf(next)
	ot := reflect.TypeFor[config.Config]()
	for i := 0; i < ot.NumField(); i++ {
		f := ot.Field(i)
		name := f.Name
		if reloadableFields[name] {
			continue
		}
		if subs, ok := reloadableSubFields[name]; ok && f.Type.Kind() == reflect.Struct {
			for j := 0; j < f.Type.NumField(); j++ {
				sub := f.Type.Field(j).Name
				if subs[sub] {
					continue
				}
				if !reflect.DeepEqual(ov.Field(i).Field(j).Interface(), nv.Field(i).Field(j).Interface()) {
					changed = append(changed, name+"."+sub)
				}
			}
			continue
		}
		if !reflect.DeepEqual(ov.Field(i).Interface(), nv.Field(i).Interface()) {
			changed = append(changed, name)
		}
	}
	return changed
}
