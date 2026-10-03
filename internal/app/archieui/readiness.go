package archieui

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
)

// newReadinessRegistry builds the readiness probes for the Gateway and State
// Store.
func newReadinessRegistry(o Options, tasks storecontract.TaskStore, chat messaging.ChatContract, problems []infraaccess.Problem) *health.Registry {
	return health.NewRegistry(
		readiness.NewProblemProbe("access_policies", problems),
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
