// state_store_recovery.go composes the offline recovery subcommands of the
// standalone archie-state-store process. They exist because the control plane
// fails closed: a stored setting that will not validate stops archied
// starting, and the in-band remedy -- replaying an earlier revision while the
// State Store is up -- is unavailable in exactly the case that needs it
// (docs/architecture/safe-change-and-recovery.md).
//
// Every operation but import works on the PostgreSQL database database_url
// names (state_store_recovery_postgres.go). The ones that write refuse while a
// serving process owns the store. Backup is the exception and deliberately so:
// the update installer cannot stop the process that runs it, so the snapshot
// that makes a failed update recoverable is taken against a serving store
// through a consistent copy.
//
// The control-plane server both validate and rollback read through is built by
// openStateStoreControlPlane -- the same root helper the served path uses -- so
// the offline checks resolve the same workflow step vocabulary as the process
// whose verdict they predict, and a constructor error is a refusal here rather
// than a server that would validate against a different vocabulary.
package archied

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// The audit identity every offline rollback records. A rollback is one more
// revision of the resource, written through the ordinary replace, so the
// operator can see who went back and when -- and can go forward again the same
// way, with the State Store up or down.
const (
	OfflineRollbackSource = "archie-state-store rollback"
	OfflineRollbackActor  = "operator:offline-recovery"
)

// Recovery operations, as named on the command line.
const (
	RecoveryBackup   = "backup"
	RecoveryRestore  = "restore"
	RecoveryValidate = "validate"
	RecoveryRollback = "rollback"
)

// StateStoreRecoveryOptions are the process inputs for one offline recovery
// operation.
type StateStoreRecoveryOptions struct {
	// Operation is one of backup, restore, validate, rollback.
	Operation string
	// Out is the snapshot backup writes.
	Out string
	// From is the snapshot restore reads.
	From string
	// Config and Overlay are the configuration the daemon would boot with.
	// validate asks whether the stored settings are a state archied starts on,
	// and that question is about the stored values *layered onto this
	// configuration*, so the file config is part of the answer.
	Config  string
	Overlay string
	// Kind is the control-plane resource kind rollback replays.
	Kind string
	// Revision is the revision rollback replays. Zero means the newest
	// revision older than the one the resource carries now.
	Revision int64
}

// RunStateStoreRecovery performs one offline operation on the task database and
// returns the line the command reports on stdout.
func RunStateStoreRecovery(ctx context.Context, options StateStoreRecoveryOptions) (string, error) {
	return runPostgresRecovery(ctx, options)
}

// checkBootGate runs the writer's per-resource validation over what is stored,
// then boot's gate over the file config with the stored resources layered on,
// and returns how many stored resources validated.
func checkBootGate(ctx context.Context, server *controlplane.Server, stored bool, options StateStoreRecoveryOptions) (int, error) {
	checked := 0
	if stored {
		var err error
		if checked, err = server.ValidateStored(ctx); err != nil {
			return 0, err
		}
	}
	base, err := bootConfig(ctx, options)
	if err != nil {
		return 0, err
	}
	document := base
	if stored {
		document, _, err = server.StoredRuntimeConfig(ctx, base)
		if err != nil {
			return 0, err
		}
	}
	if err := configuration.Validate(&document); err != nil {
		// The daemon's own wording, so an operator can match this failure to the
		// one that stopped archied starting.
		return 0, fmt.Errorf("validate database settings: %w", err)
	}
	return checked, nil
}

// bootConfig resolves the configuration the daemon would boot with. validate is
// the only recovery command that asks a question about the process rather than
// the file, and that question is about the stored values layered onto this
// document, so the document is part of the answer.
//
// It reads that config with the daemon's stderr logger: resolving it is a
// diagnosis, and a diagnosis that opened, appended to or rotated cfg.Log.File
// would edit the deployment it is inspecting.
func bootConfig(ctx context.Context, options StateStoreRecoveryOptions) (config.Config, error) {
	b := newBootstrap()
	b.stderrLog = true
	defer b.cleanup()
	if err := b.loadConfig(ctx, options.Config, options.Overlay); err != nil {
		return config.Config{}, err
	}
	return b.cfg, nil
}

// replayRevision puts back the revision options names through the control
// plane's ordinary replace. The caller holds the store's ownership claim.
func replayRevision(ctx context.Context, resources controlplane.ResourceStore, options StateStoreRecoveryOptions) (string, error) {
	server, err := openStateStoreControlPlane(resources)
	if err != nil {
		return "", err
	}
	if !server.Owns(options.Kind) {
		return "", fmt.Errorf("unknown resource kind %q", options.Kind)
	}
	current, err := resources.Resource(ctx, options.Kind)
	if errors.Is(err, storecontract.ErrResourceNotFound) {
		return "", fmt.Errorf("%s has no stored value to roll back", options.Kind)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", options.Kind, err)
	}
	revision, err := revisionToReplay(ctx, resources, options.Kind, current.Version, options.Revision)
	if err != nil {
		return "", err
	}
	replaced, err := server.Command(ctx, &pb.CommandRequest{
		Kind:            options.Kind,
		Command:         "replace",
		ValueJson:       revision.Value,
		ExpectedVersion: current.Version,
		Actor:           OfflineRollbackActor,
		Source:          OfflineRollbackSource,
		RequestId:       fmt.Sprintf("recovery-rollback-%s-%d", options.Kind, time.Now().UTC().UnixNano()),
	})
	if err != nil {
		return "", fmt.Errorf("replay %s at revision %d: %w", options.Kind, revision.Version, err)
	}
	return fmt.Sprintf("rolled back %s from version %d to the value recorded at version %d; the store now holds version %d",
		options.Kind, current.Version, revision.Version, replaced.GetResource().GetVersion()), nil
}

// revisionToReplay picks the value to put back: the revision the operator
// named, or the newest one older than the value the resource carries now.
func revisionToReplay(ctx context.Context, st controlplane.ResourceStore, kind string, current, requested int64) (storecontract.Resource, error) {
	history, err := st.ResourceHistory(ctx, kind, 0)
	if err != nil {
		return storecontract.Resource{}, fmt.Errorf("read %s history: %w", kind, err)
	}
	if requested > 0 {
		for _, revision := range history {
			if revision.Version != requested {
				continue
			}
			if revision.Version == current {
				return storecontract.Resource{}, fmt.Errorf("%s revision %d is the current version; there is nothing to roll back to", kind, requested)
			}
			return revision, nil
		}
		return storecontract.Resource{}, fmt.Errorf("%s has no revision %d", kind, requested)
	}
	for _, revision := range history {
		if revision.Version < current {
			return revision, nil
		}
	}
	return storecontract.Resource{}, fmt.Errorf("%s has no earlier revision to roll back to", kind)
}
