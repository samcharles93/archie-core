package archieui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
)

// newReadinessRegistry builds the readiness probes for the Gateway and State
// Store.
func newReadinessRegistry(o Options, tasks storecontract.TaskStore, chat messaging.ChatContract, chain *infraaccess.Live) *health.Registry {
	return health.NewRegistry(
		readiness.NewProblemProbe("access_policies", problemSource(chain)),
		captureRefusalProbe{store: refusalStore(tasks)},
		readiness.NewContractProbe("state_db", o.DependencyTimeout, func(ctx context.Context) error {
			_, err := tasks.StatusCounts(ctx)
			return err
		}),
		readiness.NewContractProbe("gateway", o.DependencyTimeout, func(ctx context.Context) error {
			_, err := chat.Snapshot(ctx)
			return err
		}),
	)
}

// problemSource keeps a missing chain a nil source, which the probe reports
// as not wired.
func problemSource(chain *infraaccess.Live) readiness.PolicyProblems {
	if chain == nil {
		return nil
	}
	return chain
}

// captureRefusalProbe degrades when any source refused more events than it
// accepted over the last hour: a sender being blocked, or someone probing.
type captureRefusalProbe struct {
	store storecontract.CaptureRefusalStore
}

func (captureRefusalProbe) Name() string { return "capture_refusals" }

func (p captureRefusalProbe) Check(ctx context.Context) health.Result {
	if p.store == nil {
		return health.Result{Status: health.StatusOK}
	}
	list, err := p.store.CaptureRefusals(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		return health.Result{Status: health.StatusDegraded, Detail: "capture refusals unavailable: " + err.Error()}
	}
	var noisy []string
	for _, c := range list {
		if c.Refused > c.Accepted {
			noisy = append(noisy, fmt.Sprintf("%s refused %d, accepted %d", c.Source, c.Refused, c.Accepted))
		}
	}
	if len(noisy) == 0 {
		return health.Result{Status: health.StatusOK}
	}
	return health.Result{Status: health.StatusDegraded, Detail: strings.Join(noisy, "; ")}
}
