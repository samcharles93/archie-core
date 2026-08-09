package main

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
// apply is called only with a Document that passed validation, and the
// running config is left untouched on error. That "Set is not called on
// the error path" property is the safety core of the reload feature --
// a bad file must leave the daemon running on the previous config.
type ReloadController struct {
	loader  *configuration.Loader
	base    string
	overlay string

	// apply receives a freshly loaded, validated Document. It is the
	// composition root's job to wire this to whatever owns the running
	// config (the daemon's Holder, the dashboard's provenance). Must be
	// non-nil; newReloadController requires it.
	apply func(*configuration.Document)

	status atomic.Pointer[config.ReloadStatus]
}

func newReloadController(loader *configuration.Loader, base, overlay string, apply func(*configuration.Document)) *ReloadController {
	c := &ReloadController{loader: loader, base: base, overlay: overlay, apply: apply}
	c.status.Store(&config.ReloadStatus{})
	return c
}

// Reload re-runs the configuration load. On validation failure it
// records the error in Status and returns it; apply is not called and
// the running configuration is unchanged. On success it calls apply
// with the fresh Document and records the reload time.
func (c *ReloadController) Reload() error {
	doc, err := c.loader.Resolve(c.base, c.overlay)
	if err != nil {
		c.status.Store(&config.ReloadStatus{
			LastError:   err.Error(),
			LastErrorAt: time.Now().UTC().Format(time.RFC3339),
		})
		return err
	}
	c.apply(doc)
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
			if err := c.Reload(); err != nil {
				log.Error("config reload failed; keeping running config", "err", err)
			}
		}
	}
}

// reloadableFields are the Config fields the daemon re-reads per cycle
// or per dispatched task, so a reload takes effect without a restart.
// This is deliberately an allowlist: a field added to Config later
// defaults to requires-restart, which is the safe direction and forces
// whoever adds it to decide deliberately.
var reloadableFields = map[string]bool{
	// Tickers reset on change (daemon.go Run / runIdentities).
	"PollInterval": true,
	// Poll loops re-read per cycle (daemon.go poll / pollForIdentity).
	"Repos": true,
	// Per-task snapshot: configFor -> executionFor -> d.Cfg.Get() at
	// dispatch, carried by TaskConfig (config.go ForTask).
	"DiffCapLines": true,
	"Budgets":      true,
	// Dispatch is read per poll (daemon.go pollIssuesWithConfig switch)
	// and carried per task like DiffCapLines.
	"Dispatch": true,
}

// reloadableSubFields are sub-fields of structs that are otherwise
// startup-built. VolumeTTL and MaxConcurrency are re-read per cycle
// (cleanupExpiredStorage, drainSQLite/drainNATS); everything else in
// Containers (Image, Enabled, MaxUptime, PullPolicy, Network) is frozen
// in the startup-built container pool.
var reloadableSubFields = map[string]map[string]bool{
	"Containers": {"VolumeTTL": true, "MaxConcurrency": true},
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
