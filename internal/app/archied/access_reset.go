// The `archied access reset` command: the locked-out-org and broken-instance
// recovery surface (docs/prds/orgs-and-access.md, "Recovering from a
// locked-out org"). It restores the shipped role policies for one org and
// removes its other org-level policies, or removes the stored instance
// policies; the cross-org forbid is engine-enforced and survives any reset.
//
// It runs only on the State Store host, never over the network, and is
// recorded as an audit event. It claims the same store ownership the State
// Store holds, so a running store refuses it: stop the store, reset, start
// the store.
package archied

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// accessResetCommand is the argv selector for the recovery command.
const accessResetCommand = "access"

// IsAccessResetArgs reports whether args selects the access recovery command.
func IsAccessResetArgs(args []string) bool {
	return len(args) > 2 && args[0] == accessResetCommand && args[1] == resetCommand
}

const resetCommand = "reset"

// RunAccessReset performs the reset and returns the process exit code:
// 0 reset, 1 failure, 2 usage.
func RunAccessReset(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet(resetCommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", configuration.DefaultConfigPath(), "path to a TOML/YAML config file or configuration directory")
	overlayPath := fs.String("config-overlay", "", "path to a TOML/YAML overlay file or configuration directory applied on top of -config")
	orgFlag := fs.String("org", "", "org whose access to reset; conflicts with --instance")
	instance := fs.Bool("instance", false, "reset the stored instance policies")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "reset: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if (*orgFlag != "" && *instance) || (*orgFlag == "" && !*instance) {
		fmt.Fprintln(stderr, "reset: give exactly one of --org <org> or --instance")
		return 2
	}

	b := newBootstrap()
	defer b.cleanup()
	if err := b.loadConfig(ctx, *cfgPath, *overlayPath); err != nil {
		return 1
	}
	if err := b.openStateStorePool(ctx); err != nil {
		// A running State Store holds the ownership claim; the reset must not
		// race it. Stop the store, reset, start the store.
		fmt.Fprintln(stderr, "reset: open the State Store database (stop archie-state-store first)")
		return 1
	}
	resetter, ok := b.st.(access.Resetter)
	if !ok {
		fmt.Fprintln(stderr, "reset: the State Store does not support the access reset surface")
		return 1
	}
	var err error
	switch {
	case *instance:
		err = resetter.ResetInstancePolicies(ctx)
	default:
		err = resetter.ResetOrgPolicies(ctx, org.OrgID(*orgFlag))
	}
	if err != nil {
		fmt.Fprintf(stderr, "reset: %v\n", err)
		return 1
	}
	if err := recordResetAudit(ctx, b.pg, *orgFlag, *instance); err != nil {
		fmt.Fprintf(stderr, "reset: audit: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "reset: access policies for %s restored to the shipped set\n", resetTarget(*orgFlag, *instance))
	return 0
}

// resetTarget names what the reset covered, for the operator's output.
func resetTarget(orgID string, instance bool) string {
	if instance {
		return "the instance"
	}
	return fmt.Sprintf("org %q", orgID)
}

// recordResetAudit writes the one audit row the reset is recorded as. The
// policy changes themselves carry their own sys_audit rows; this names the
// recovery.
func recordResetAudit(ctx context.Context, pool *pgxpool.Pool, orgID string, instance bool) error {
	key := "reset/instance"
	if !instance {
		key = fmt.Sprintf("reset/org/%s", orgID)
	}
	return postgres.New(pool).RecordResetAudit(ctx, key)
}
