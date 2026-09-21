// state_store_recovery.go composes the offline recovery subcommands of the
// standalone archie-state-store process. They exist because the control plane
// fails closed: a stored setting that will not validate stops archied
// starting, and the in-band remedy -- replaying an earlier revision while the
// State Store is up -- is unavailable in exactly the case that needs it
// (docs/architecture/safe-change-and-recovery.md).
//
// Every operation works on the task database file directly, with the State
// Store stopped, and the ones that write refuse when another process owns the
// file. Backup is the exception and deliberately so: the update installer
// cannot stop the process that runs it, so the snapshot that makes a failed
// update reversible is taken against a serving store through SQLite's own
// consistent copy.
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

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/store"
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
	// DB is the task database file the State Store owns: the configured
	// db_path with "-tasks.sqlite" appended, not the configured path itself.
	DB string
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
	switch options.Operation {
	case RecoveryBackup:
		if options.DB == "" || options.Out == "" {
			return "", errors.New("backup requires -db and -out")
		}
		if err := store.Backup(ctx, options.DB, options.Out); err != nil {
			return "", err
		}
		return fmt.Sprintf("backed up %s to %s", options.DB, options.Out), nil
	case RecoveryRestore:
		if options.DB == "" || options.From == "" {
			return "", errors.New("restore requires -db and -from")
		}
		if err := store.Restore(ctx, options.DB, options.From); err != nil {
			return "", err
		}
		return fmt.Sprintf("restored %s from %s; start the State Store again", options.DB, options.From), nil
	case RecoveryValidate:
		if options.DB == "" {
			return "", errors.New("validate requires -db")
		}
		return validateStore(ctx, options)
	case RecoveryRollback:
		if options.DB == "" || options.Kind == "" {
			return "", errors.New("rollback requires -db and -kind")
		}
		return rollbackResource(ctx, options)
	default:
		return "", fmt.Errorf("unknown recovery command %q", options.Operation)
	}
}

// validateStore answers the question an operator actually has: would archied
// start against this file. It runs the checks that stop it, in the order boot
// meets them.
//
// First the file itself, since a database that is corrupt, newer than this
// binary, or not a database is one the serving process refuses to open. Then
// the writer's own per-resource validation, which is what a hand-edited value
// violated. Then boot's gate: the file config with every stored resource
// layered onto it, and configuration.Validate -- the check whose failure the
// daemon reports as "validate database settings".
//
// The two validation layers disagree in both directions, and both are reported
// rather than merged. The write path is stricter about shape (it rejects
// unknown fields boot's decode ignores), while boot is stricter about meaning
// (only boot checks dispatch.trigger and a positive poll interval, both of
// which the write path's own validators let through). A store either check
// refuses is a store somebody has to look at.
func validateStore(ctx context.Context, options StateStoreRecoveryOptions) (string, error) {
	version, err := store.ValidateFile(ctx, options.DB)
	if err != nil {
		return "", err
	}
	st, err := store.OpenReadOnly(ctx, options.DB)
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()

	server, err := openStateStoreControlPlane(st)
	if err != nil {
		return "", err
	}
	// Is there a resources table at all? A store written before the control plane
	// existed has none, and the State Store creates it and seeds every kind from
	// this same config on its next start -- so there is nothing stored to check
	// with the writer's validator, and boot's gate is the config check alone.
	// That is the same stance the layering takes for a kind the table is missing:
	// what the daemon reads for a kind nothing was ever stored for is the seed.
	checked := 0
	stored, err := st.StoresResources(ctx)
	if err != nil {
		return "", err
	}
	if stored {
		if checked, err = server.ValidateStored(ctx); err != nil {
			return "", err
		}
	}

	base, err := bootConfig(ctx, options)
	if err != nil {
		return "", err
	}
	document := base
	if stored {
		document, _, err = server.StoredRuntimeConfig(ctx, base)
		if err != nil {
			return "", err
		}
	}
	if err := configuration.Validate(&document); err != nil {
		// The daemon's own wording, so an operator can match this failure to the
		// one that stopped archied starting.
		return "", fmt.Errorf("validate database settings: %w", err)
	}
	return fmt.Sprintf("%s is a valid store at schema version %d; %d stored resources validate", options.DB, version, checked), nil
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

// rollbackResource is the one operation the Web UI cannot be asked to perform
// here: the daemon fails closed on the bad value, so the store that would serve
// the rollback is the store that will not start. It replays the revision's own
// value through the ordinary replace, so no rollback RPC exists or is needed,
// and the rollback is audited as one more revision.
func rollbackResource(ctx context.Context, options StateStoreRecoveryOptions) (string, error) {
	// Before the lock and before the open: opening a store creates the file,
	// and a rollback pointed at a mistyped path must not leave an empty
	// database where the operator expected theirs.
	if err := store.RequireDatabase(options.DB); err != nil {
		return "", err
	}
	ownership, err := store.AcquireOwnership(options.DB)
	if err != nil {
		return "", err
	}
	defer func() { _ = ownership.Release() }()

	st, err := store.Open(ctx, options.DB)
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()

	server, err := openStateStoreControlPlane(st)
	if err != nil {
		return "", err
	}
	if !server.Owns(options.Kind) {
		return "", fmt.Errorf("unknown resource kind %q", options.Kind)
	}
	current, err := st.Resource(ctx, options.Kind)
	if errors.Is(err, store.ErrResourceNotFound) {
		return "", fmt.Errorf("%s has no stored value to roll back", options.Kind)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", options.Kind, err)
	}
	revision, err := revisionToReplay(ctx, st, options.Kind, current.Version, options.Revision)
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
func revisionToReplay(ctx context.Context, st *store.Store, kind string, current, requested int64) (store.Resource, error) {
	history, err := st.ResourceHistory(ctx, kind, 0)
	if err != nil {
		return store.Resource{}, fmt.Errorf("read %s history: %w", kind, err)
	}
	if requested > 0 {
		for _, revision := range history {
			if revision.Version != requested {
				continue
			}
			if revision.Version == current {
				return store.Resource{}, fmt.Errorf("%s revision %d is the current version; there is nothing to roll back to", kind, requested)
			}
			return revision, nil
		}
		return store.Resource{}, fmt.Errorf("%s has no revision %d", kind, requested)
	}
	for _, revision := range history {
		if revision.Version < current {
			return revision, nil
		}
	}
	return store.Resource{}, fmt.Errorf("%s has no earlier revision to roll back to", kind)
}
