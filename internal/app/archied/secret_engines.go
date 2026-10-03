package archied

import (
	"context"

	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/secretengine"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
)

// startSecretEngines launches the enabled secret-engine extensions once the
// State Store client exists, so the runtime layering that follows can resolve
// references that name one.
func (b *boot) startSecretEngines(ctx context.Context, client *staterpc.Client) {
	source := secretengine.Source{Query: b.controlPlane, Packages: client}
	report := func(ctx context.Context, version int64, err error) {
		b.applyStatus.Report(ctx, controlplanerpc.ExtensionSettingsKind, version, err)
	}
	runner := secretengine.Supervise(ctx, b.secrets, source, b.processName, report, b.log)
	b.addCleanup(runner.Close)
}
