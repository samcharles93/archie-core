package archiemessaging

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
)

// newReadinessRegistry builds the health registry for the Messaging Service.
// Messaging is ready when the Gateway Service responds to Snapshot within
// the dependency timeout.
func newReadinessRegistry(o Options, chat messaging.ChatContract) *health.Registry {
	return health.NewRegistry(
		readiness.NewContractProbe("gateway", o.DependencyTimeout, func(ctx context.Context) error {
			_, err := chat.Snapshot(ctx)
			return err
		}),
	)
}
