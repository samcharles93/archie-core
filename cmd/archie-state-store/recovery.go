package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/samcharles93/archie-core/internal/app/archied"
)

// recoveryUsage is the whole flag surface of the offline recovery commands.
// They are subcommands of this binary rather than a recovery binary of their
// own: offline database maintenance belongs to the process that owns the file
// and its path layout (docs/architecture/organisation.md#process-boundaries).
const recoveryUsage = `usage: archie-state-store <command> [flags]

With no command, archie-state-store serves the State Store gRPC contract.

The recovery commands operate on the task database file directly -- the
configured db_path with "-tasks.sqlite" appended -- and are how an operator
recovers a database the control plane will not start on. Stop the State Store
first: restore and rollback rewrite the file and refuse while another process
owns it, while backup takes a consistent snapshot of a serving store.

  backup   -db FILE -out FILE       write a snapshot of the database
  restore  -db FILE -from FILE      replace the database with a snapshot
  validate -db FILE                 check the database the way boot does
  rollback -db FILE -kind KIND      replay an earlier revision of a stored
           [-revision N]            resource through the ordinary replace
`

// runRecovery parses and performs one offline recovery command, reporting the
// operation's summary on stdout. Exit codes follow the serve path: 0 succeeded,
// 1 the operation failed, 2 the command line was wrong.
func runRecovery(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, recoveryUsage)
		return 2
	}
	command := args[0]
	options := archied.StateStoreRecoveryOptions{Operation: command}
	switch command {
	case archied.RecoveryBackup, archied.RecoveryRestore, archied.RecoveryValidate, archied.RecoveryRollback:
	default:
		fmt.Fprintf(stderr, "archie-state-store: unknown command %q\n\n", command)
		fmt.Fprint(stderr, recoveryUsage)
		return 2
	}

	flags := flag.NewFlagSet("archie-state-store "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, recoveryUsage) }
	flags.StringVar(&options.DB, "db", "", "task database file the State Store owns (<db_path>-tasks.sqlite)")
	flags.StringVar(&options.Out, "out", "", "snapshot file backup writes")
	flags.StringVar(&options.From, "from", "", "snapshot file restore reads")
	flags.StringVar(&options.Kind, "kind", "", "control-plane resource kind rollback replays")
	flags.Int64Var(&options.Revision, "revision", 0, "revision rollback replays (default: the newest one older than the current value)")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "archie-state-store %s: unexpected argument %q\n", command, flags.Arg(0))
		return 2
	}

	summary, err := archied.RunStateStoreRecovery(context.Background(), options)
	if err != nil {
		fmt.Fprintf(stderr, "archie-state-store %s: %v\n", command, err)
		return 1
	}
	fmt.Fprintln(stdout, summary)
	return 0
}
