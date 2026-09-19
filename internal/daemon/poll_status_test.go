package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestPollPassStampsLastPollAt pins the "is the poller still running" fact
// /status reports: the daemon stamps the moment a poll pass begins, so a
// missing or stale timestamp is what tells an operator the poll loop has
// stopped or is stuck inside a pass.
//
// Both poll paths are covered because they are separate goroutines with
// separate entry points: Run's single-identity loop drives poll, while
// runIdentities drives one pollForIdentity per identity. A stamp on only one
// of them leaves /status permanently reporting "never" for the other
// deployment shape.
func TestPollPassStampsLastPollAt(t *testing.T) {
	ctx := context.Background()
	repo := config.Repo{Owner: "acme", Name: "widget"}

	pollPaths := []struct {
		name string
		poll func(context.Context, *Daemon)
	}{
		{
			name: "single-identity poll",
			poll: func(ctx context.Context, d *Daemon) { d.poll(ctx) },
		},
		{
			name: "per-identity poll",
			poll: func(ctx context.Context, d *Daemon) {
				d.pollForIdentity(ctx, &IdentityRunner{
					Name:  "main",
					Forge: d.Forge,
					Repos: d.Cfg.Get().Repos,
					Log:   d.Log,
				})
			},
		},
	}

	for _, path := range pollPaths {
		t.Run(path.name, func(t *testing.T) {
			d := &Daemon{
				Cfg: config.NewHolder(config.Config{
					BotUser:  "archie",
					Repos:    []config.Repo{repo},
					Dispatch: config.Dispatch{Trigger: "assignee"},
				}),
				Forge: &pollingForge{},
				Log:   slog.New(slog.DiscardHandler),
			}

			if got := d.LastPollAt(); !got.IsZero() {
				t.Fatalf("LastPollAt() before any poll pass = %v, want the zero time", got)
			}

			before := time.Now()
			path.poll(ctx, d)
			after := time.Now()

			got := d.LastPollAt()
			if got.IsZero() {
				t.Fatal("LastPollAt() after a poll pass = zero, want the pass's start time")
			}
			if got.Before(before) || got.After(after) {
				t.Errorf("LastPollAt() = %v, want a time within [%v, %v]", got, before, after)
			}
		})
	}
}
