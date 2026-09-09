package archieui

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
)

// newReadinessRegistry builds the registry behind GET /health/detailed. The
// UI is ready when both of its mandatory remote contracts answer within the
// dependency timeout; each probe consumes the dependency's own result over
// the wire and never inspects its manager, registry or database
// (docs/prds/ui-service-boundary.md:126-132).
//
// The component names match the daemon dashboard's, so the readiness view
// reads the same whichever process serves it.
func newReadinessRegistry(o Options, tasks storecontract.TaskStore, chat messaging.ChatContract) *health.Registry {
	return health.NewRegistry(
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
